#!/usr/bin/env python3
"""Brand asset consistency (handoff_app_standardization.md 4.1).

Every app icon is generated from frontend/brand/mark.svg by frontend/scripts/brand-apps.mjs, which
also writes apps/brand-manifest.json. CI never regenerates; this check proves the committed files
still belong to the committed source and are structurally valid:

  * the source hash in the manifest is the hash of frontend/brand/mark.svg (change a colour in the
    source without regenerating and this fails);
  * every manifest file exists, is unmodified (hash) and has the stated size and container layout
    (PNG header, ICO directory, ICNS entries, BMP header);
  * the iOS light icon has no alpha channel; Android vectors parse and use only white;
  * each platform actually references its assets (Info.plist key in the build script, csproj,
    installer script, Android manifest).

Standard library only, so it runs on any CI image.
"""
import hashlib
import json
import struct
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
failures = []


def check(ok, message):
    if not ok:
        failures.append(message)
    print(('PASS  ' if ok else 'FAIL  ') + message)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def png_info(data):
    """(width, height, colour type, has tRNS) of a PNG, or None when it is not one."""
    if data[:8] != b'\x89PNG\r\n\x1a\n' or data[12:16] != b'IHDR':
        return None
    width, height = struct.unpack('>II', data[16:24])
    colour = data[25]
    offset, trns = 8, False
    while offset + 8 <= len(data):
        length, kind = struct.unpack('>I4s', data[offset:offset + 8])
        trns = trns or kind == b'tRNS'
        offset += 12 + length
    return width, height, colour, trns


def ico_frames(data):
    """[(size, payload)] of an ICO whose frames are PNGs; None when the directory is invalid."""
    if len(data) < 6 or struct.unpack('<HHH', data[:6])[:2] != (0, 1):
        return None
    count = struct.unpack('<H', data[4:6])[0]
    frames = []
    for index in range(count):
        entry = data[6 + 16 * index:22 + 16 * index]
        if len(entry) < 16:
            return None
        width, height = entry[0] or 256, entry[1] or 256
        length, offset = struct.unpack('<II', entry[8:16])
        if offset + length > len(data):
            return None
        frames.append((width, height, data[offset:offset + length]))
    return frames


def icns_entries(data):
    if data[:4] != b'icns' or struct.unpack('>I', data[4:8])[0] != len(data):
        return None
    entries, offset = [], 8
    while offset + 8 <= len(data):
        kind, length = struct.unpack('>4sI', data[offset:offset + 8])
        if length < 8 or offset + length > len(data):
            return None
        entries.append((kind.decode('ascii'), data[offset + 8:offset + length]))
        offset += length
    return entries if offset == len(data) else None


ICNS_SIZES = {'icp4': 16, 'icp5': 32, 'ic11': 32, 'ic12': 64, 'ic07': 128, 'ic13': 256, 'ic08': 256,
              'ic14': 512, 'ic09': 512, 'ic10': 1024}


def verify_file(entry, data):
    path, kind = entry['path'], entry['kind']
    if kind == 'png':
        info = png_info(data)
        ok = info is not None and info[:2] == (entry['width'], entry['height'])
        check(ok, '%s is a %dx%d PNG' % (path, entry['width'], entry['height']))
        if entry.get('opaque'):
            check(info is not None and info[2] in (0, 2) and not info[3], '%s has no alpha channel' % path)
    elif kind == 'bmp':
        ok = data[:2] == b'BM' and struct.unpack('<ii', data[18:26]) == (entry['width'], entry['height']) \
            and struct.unpack('<H', data[28:30])[0] == 24
        check(ok, '%s is a %dx%d 24-bit BMP' % (path, entry['width'], entry['height']))
    elif kind == 'ico':
        frames = ico_frames(data)
        ok = frames is not None and [f[0] for f in frames] == entry['sizes'] and all(
            (png_info(f[2]) or (0, 0))[:2] == (f[0], f[1]) for f in frames)
        check(ok, '%s holds PNG frames %s' % (path, entry['sizes']))
    elif kind == 'icns':
        entries = icns_entries(data)
        ok = entries is not None and {k for k, _ in entries} == set(ICNS_SIZES) and all(
            (png_info(p) or (0, 0))[:2] == (ICNS_SIZES[k],) * 2 for k, p in entries)
        check(ok, '%s is a valid ICNS with every size up to 1024' % path)
    elif kind == 'android-vector':
        try:
            root = ET.fromstring(data.decode('utf-8'))
            ns = '{http://schemas.android.com/apk/res/android}'
            colours = {e.get(ns + a) for e in root.iter() for a in ('fillColor', 'strokeColor') if e.get(ns + a)}
            ok = root.tag == 'vector' and colours == {'#FFFFFFFF'} and root.get(ns + 'viewportWidth') is not None
        except (ET.ParseError, UnicodeDecodeError):
            ok = False
        check(ok, '%s is a vector drawable that uses only white' % path)
    else:
        check(False, '%s has an unknown kind %r' % (path, kind))


def main():
    manifest = json.loads((ROOT / 'apps/brand-manifest.json').read_text())
    source = (ROOT / manifest['source']['path']).read_bytes()
    check(sha256(source) == manifest['source']['sha256'],
          'frontend/brand/mark.svg matches the manifest (regenerate with npm run brand:apps after changing the logo)')
    check((ROOT / 'frontend/src/app/icon.svg').read_bytes() == source, 'the website icon.svg is the same source')

    paths = []
    for entry in manifest['files']:
        path = ROOT / entry['path']
        paths.append(entry['path'])
        if not path.is_file():
            check(False, '%s exists' % entry['path'])
            continue
        data = path.read_bytes()
        check(sha256(data) == entry['sha256'], '%s is the generated file (hash)' % entry['path'])
        verify_file(entry, data)

    expected = {
        'apps/macos/Branding/AppIcon.icns', 'apps/windows/AsterLink.App/Assets/AsterLink.ico',
        'apps/windows/AsterLink.App/Assets/tray-idle.ico', 'apps/windows/AsterLink.App/Assets/tray-live.ico',
        'apps/windows/AsterLink.App/Assets/tray-error.ico', 'apps/windows/Packaging/wizard-small-55.bmp',
        'apps/windows/Packaging/wizard-small-110.bmp', 'apps/ios/AsterLink/Assets.xcassets/AppIcon.appiconset/light.png',
        'apps/ios/AsterLink/Assets.xcassets/AppIcon.appiconset/dark.png', 'apps/ios/AsterLink/Assets.xcassets/AppIcon.appiconset/tinted.png',
        'apps/ios/AsterLink/Assets.xcassets/LaunchMark.imageset/launch-mark.png',
        'apps/android/app/src/main/res/drawable/ic_launcher_foreground.xml',
        'apps/android/app/src/main/res/drawable/ic_notification.xml',
    } | {'apps/macos/Branding/MenuBar%sTemplate%s.png' % (s, r) for s in ('Idle', 'Active', 'Error') for r in ('', '@2x')}
    check(expected <= set(paths), 'the manifest lists every expected asset (missing: %s)' % sorted(expected - set(paths)))

    # Each platform really uses its assets.
    def text(path):
        return (ROOT / path).read_text(encoding='utf-8-sig')

    build = text('scripts/build-native-macos.py')
    check("'CFBundleIconFile': 'AppIcon'" in build and "(macos / 'Branding')" in build,
          'the macOS build writes CFBundleIconFile and copies Branding into Resources')
    check('MenuBarIdleTemplate' in text('apps/macos/Sources/AsterLinkApp/AsterLinkApp.swift'), 'the macOS menu bar uses the template images')
    csproj = text('apps/windows/AsterLink.App/AsterLink.App.csproj')
    check('<ApplicationIcon>Assets\\AsterLink.ico</ApplicationIcon>' in csproj
          and all('Assets\\tray-%s.ico' % s in csproj for s in ('idle', 'live', 'error')),
          'the Windows project sets ApplicationIcon and embeds the tray icons')
    check('Assets/AsterLink.ico' in text('apps/windows/AsterLink.App/MainWindow.xaml'), 'the Windows main window uses the brand icon')
    iss = text('apps/windows/Packaging/setup.iss')
    check('SetupIconFile=..\\AsterLink.App\\Assets\\AsterLink.ico' in iss and 'wizard-small-55.bmp' in iss,
          'the Windows installer uses the brand icon and wizard images')
    manifest_xml = text('apps/android/app/src/main/AndroidManifest.xml')
    check('@drawable/ic_notification' in manifest_xml and 'ic_vpn_key' not in manifest_xml,
          'the Android Quick Settings tile uses the brand silhouette')
    check('ic_launcher_foreground' in text('apps/android/app/src/main/res/mipmap-anydpi/ic_launcher.xml')
          and '<monochrome' in text('apps/android/app/src/main/res/mipmap-anydpi/ic_launcher.xml'),
          'the Android adaptive icon has foreground and monochrome layers')

    print('\n%d check(s) failed' % len(failures) if failures else '\nall brand asset checks passed')
    return 1 if failures else 0


if __name__ == '__main__':
    sys.exit(main())
