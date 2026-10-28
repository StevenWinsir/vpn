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
    installer script, Android manifest);
  * palette (handoff_app_standardization.md 4.2): every colour role of the iOS and Android themes
    equals the website token it maps to, in the light and the dark scheme (change a token in
    frontend/src/app/globals.css and this fails until the apps follow).

Standard library only, so it runs on any CI image.
"""
import hashlib
import json
import re
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


def text(path):
    return (ROOT / path).read_text(encoding='utf-8-sig')


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

# Website token -> (iOS Theme.swift role, Android BrandTokens field). The apps are checked in full for
# these roles; the website has many more tokens (home page, banners) that an app has no use for.
PALETTE = {
    'brand': ('brand', 'brand'), 'brand-fill': ('brandFill', 'brandFill'), 'on-brand': ('onBrand', 'onBrand'),
    'brand-soft': ('brandSoft', 'brandSoft'), 'bg': ('background', 'bg'), 'surface': ('surface', 'surface'),
    'surface-2': ('surfaceMuted', 'surface2'), 'border': ('stroke', 'border'),
    'border-strong': ('strokeStrong', 'borderStrong'), 'text': ('textPrimary', 'text'),
    'text-2': ('textSecondary', 'text2'), 'text-3': ('textTertiary', 'text3'),
    'warn-text': ('warnText', 'warnText'), 'warn-bg': ('warnBackground', 'warnBg'),
    'warn-border': ('warnBorder', 'warnBorder'), 'danger-text': ('dangerText', 'dangerText'),
    'danger-bg': ('dangerBackground', 'dangerBg'), 'focus': ('focus', 'focus'),
}


def parse_colour(value):
    """'#rrggbb' or 'rgba(r, g, b, a)' -> (r, g, b, alpha 0..1); None when it is neither."""
    value = value.strip().lower()
    match = re.fullmatch(r'#([0-9a-f]{6})', value)
    if match:
        raw = int(match.group(1), 16)
        return raw >> 16, (raw >> 8) & 255, raw & 255, 1.0
    match = re.fullmatch(r'rgba\(\s*(\d+),\s*(\d+),\s*(\d+),\s*([0-9.]+)\s*\)', value)
    if match:
        return int(match.group(1)), int(match.group(2)), int(match.group(3)), float(match.group(4))
    return None


def website_tokens(css):
    """{'light': {token: colour}, 'dark': {...}} from the tokens block of globals.css."""
    block = css[css.index('tokens:start'):css.index('/* tokens:end')]
    parts = block.split("[data-mantine-color-scheme='dark']")
    schemes = {'light': parts[0], 'dark': parts[1]}
    return {name: {m.group(1): parse_colour(m.group(2)) for m in re.finditer(r'--al-([a-z0-9-]+):\s*([^;]+);', body)
                   if parse_colour(m.group(2))} for name, body in schemes.items()}


def same_colour(a, b):
    return a is not None and b is not None and a[:3] == b[:3] and abs(a[3] - b[3]) <= 0.01


def luminance(colour):
    def channel(value):
        value /= 255
        return value / 12.92 if value <= 0.03928 else ((value + 0.055) / 1.055) ** 2.4
    return 0.2126 * channel(colour[0]) + 0.7152 * channel(colour[1]) + 0.0722 * channel(colour[2])


def contrast(foreground, background):
    high, low = sorted((luminance(foreground), luminance(background)), reverse=True)
    return (high + 0.05) / (low + 0.05)


def over(colour, background):
    """colour (r, g, b, alpha) laid over an opaque background (r, g, b)."""
    return tuple(round(colour[i] * colour[3] + background[i] * (1 - colour[3])) for i in range(3))


# (foreground token, background token): the pairs the apps draw text on. 4.5 is WCAG AA for body text.
CONTRAST_PAIRS = [('text', 'surface'), ('text', 'bg'), ('text-2', 'surface'), ('text-3', 'surface'), ('brand', 'surface'),
                  ('brand', 'bg'), ('brand', 'brand-soft'), ('on-brand', 'brand-fill'), ('warn-text', 'warn-bg'),
                  ('danger-text', 'danger-bg'), ('danger-text', 'surface')]


def check_contrast(tokens):
    for scheme in ('light', 'dark'):
        surface = tokens[scheme]['surface'][:3]
        for foreground, background in CONTRAST_PAIRS:
            base = tokens[scheme][background]
            ratio = contrast(tokens[scheme][foreground][:3], over(base, surface) if base[3] < 1 else base[:3])
            check(ratio >= 4.5, 'contrast %s on %s (%s) is %.2f, at least 4.5' % (foreground, background, scheme, ratio))


def check_palette(tokens):
    ios = text('apps/ios/AsterLink/Theme.swift')
    pattern = re.compile(r'static let (\w+) = Color\(light: 0x([0-9A-Fa-f]{6}), dark: 0x([0-9A-Fa-f]{6})'
                         r'(?:, lightAlpha: ([0-9.]+))?(?:, darkAlpha: ([0-9.]+))?\)')
    roles = {}
    for name, light, dark, light_alpha, dark_alpha in pattern.findall(ios):
        roles[name] = {'light': parse_colour('#' + light.lower()[:6])[:3] + (float(light_alpha or 1),),
                       'dark': parse_colour('#' + dark.lower()[:6])[:3] + (float(dark_alpha or 1),)}
    kotlin = text('apps/android/app/src/main/java/com/asterlink/vpn/ui/theme/Theme.kt')
    android = {}
    for scheme, const in (('light', 'LightTokens'), ('dark', 'DarkTokens')):
        body = re.search(const + r' = BrandTokens\((.*?)\n\)', kotlin, re.S).group(1)
        for field, argb in re.findall(r'(\w+) = Color\(0x([0-9A-Fa-f]{8})\)', body):
            raw = int(argb, 16)
            android.setdefault(field, {})[scheme] = ((raw >> 16) & 255, (raw >> 8) & 255, raw & 255, (raw >> 24) / 255)
    for token, (ios_role, android_field) in PALETTE.items():
        for scheme in ('light', 'dark'):
            want = tokens[scheme].get(token)
            if want is None:  # a token the dark scheme inherits from nothing: it must be defined for both
                check(False, 'the website defines --al-%s for the %s scheme' % (token, scheme))
                continue
            check(same_colour(roles.get(ios_role, {}).get(scheme), want),
                  'iOS Theme.%s (%s) is --al-%s' % (ios_role, scheme, token))
            check(same_colour(android.get(android_field, {}).get(scheme), want),
                  'Android %sTokens.%s is --al-%s' % (scheme.capitalize(), android_field, token))
    check(not re.search(r'\b(?:2F6FEB|5B8DEF)\b', ios + kotlin, re.I), 'the retired blue is gone from the iOS and Android themes')

    # The accent colour, the launch background and the Android resource colours repeat tokens.
    for asset, token in (('AccentColor', 'brand'), ('LaunchBackground', 'bg')):
        contents = json.loads(text('apps/ios/AsterLink/Assets.xcassets/%s.colorset/Contents.json' % asset))
        found = {}
        for item in contents['colors']:
            comps = item['color']['components']
            dark = any(a.get('value') == 'dark' for a in item.get('appearances', []))
            found['dark' if dark else 'light'] = tuple(int(str(comps[c]), 16) if str(comps[c]).startswith('0x') else round(float(comps[c]) * 255)
                                                       for c in ('red', 'green', 'blue')) + (1.0,)
        for scheme in ('light', 'dark'):
            check(same_colour(found.get(scheme), tokens[scheme][token]), 'iOS %s (%s) is --al-%s' % (asset, scheme, token))
    for path, scheme in (('values', 'light'), ('values-night', 'dark')):
        xml = text('apps/android/app/src/main/res/%s/colors.xml' % path)
        value = re.search(r'name="background">(#[0-9A-Fa-f]{6})<', xml)
        check(value is not None and same_colour(parse_colour(value.group(1)), tokens[scheme]['bg']),
              'Android %s/colors.xml background is --al-bg' % path)
    icon = re.search(r'name="icon_background">(#[0-9A-Fa-f]{6})<', text('apps/android/app/src/main/res/values/colors.xml'))
    check(icon is not None and same_colour(parse_colour(icon.group(1)), tokens['light']['brand-fill']),
          'Android icon_background is --al-brand-fill')


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

    tokens = website_tokens(text('frontend/src/app/globals.css'))
    check_palette(tokens)
    check_contrast(tokens)

    print('\n%d check(s) failed' % len(failures) if failures else '\nall brand asset checks passed')
    return 1 if failures else 0


if __name__ == '__main__':
    sys.exit(main())
