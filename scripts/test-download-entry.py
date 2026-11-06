#!/usr/bin/env python3
"""The packaging scripts' console entry sheet (handoff_download.md D17, DL3-13).

Checks that download-entry.txt names exactly the console form's fields, refuses an incomplete or
malformed entry, that every packaging script writes it into its private folder and none still writes
frontend/.env lines, and that release_scrub treats it as private-only.
"""
import re
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
import download_entry  # noqa: E402
import release_scrub  # noqa: E402

failures = []


def check(ok, what):
    print('%s %s' % ('PASS' if ok else 'FAIL', what))
    if not ok:
        failures.append(what)


sha, cert = 'ab' * 32, 'f5' * 32
apk = {'platform': 'android', 'arch': 'arm64-v8a', 'format': 'apk', 'app_version': '1.2.3',
       'file': 'AsterLink-1.2.3-android-arm64-v8a.apk', 'sha256': sha, 'size_bytes': 48128121, 'signing_cert_sha256': cert}
with tempfile.TemporaryDirectory() as folder:
    path = download_entry.write(Path(folder), [apk, dict(apk, arch='x86_64', file='AsterLink-1.2.3-android-x86_64.apk')])
    text = path.read_text(encoding='utf-8')
check(path.name == 'download-entry.txt', 'the sheet is download-entry.txt')
for label in ('平台：android', '架构：arm64-v8a', '格式：apk', '版本号：1.2.3', 'SHA-256：' + sha,
              '文件大小（字节）：48128121', '签名证书 SHA-256：' + cert, '[2]', '架构：x86_64'):
    check(label in text, 'the sheet carries %s' % label.split('：')[0])
check('Bundle ID' not in text, 'an APK block has no Bundle ID line')
check('https://' not in text and '.env' not in text, 'the sheet holds no address and no .env lines')

ipa = {'platform': 'ios', 'arch': 'arm64', 'format': 'ipa', 'app_version': '1.2.3', 'file': 'a.ipa', 'sha256': sha,
       'size_bytes': 1, 'bundle_id': 'cn.example.asterlink'}
check('Bundle ID：cn.example.asterlink' in download_entry.entry_text([ipa]), 'an IPA block names its bundle id')
for broken, why in ((dict(apk, sha256=''), 'missing hash'), (dict(apk, sha256='AB' * 32), 'upper-case hash'),
                    (dict(apk, size_bytes=None), 'missing size'), (dict(apk, signing_cert_sha256='f5:' * 31 + 'f5'), 'colon certificate')):
    try:
        download_entry.entry_text([broken])
        check(False, 'refuses a %s' % why)
    except ValueError:
        check(True, 'refuses a %s' % why)

# Labels must be the console form's (frontend/src/components/console/console-downloads.tsx).
form = (ROOT / 'frontend' / 'src' / 'components' / 'console' / 'console-downloads.tsx').read_text(encoding='utf-8')
for key, label in download_entry.FIELDS:
    if key in ('platform', 'arch', 'format', 'file'):
        continue
    check(('label="%s"' % label) in form, 'the console form has the field %s' % label)

for script in ('package-native-android.py', 'package-native-windows.py', 'package-native-macos.py',
               'package-native-ios.py', 'ios/resign-ipa.py'):
    source = (ROOT / 'scripts' / script).read_text(encoding='utf-8')
    check('download_entry.write(' in source, '%s writes the entry sheet' % script)
    check(not re.search(r"(ANDROID_APK|IOS_IPA|IOS_MANIFEST|_SOURCE_URL)\w*=", source) and 'website-env.txt' not in source,
          '%s prints no frontend/.env download lines' % script)

check('download-entry.txt' in release_scrub.PRIVATE_ONLY_NAMES, 'release_scrub treats the sheet as private-only')
check(release_scrub.named_leaks(['release/download-entry.txt']) == ['release/download-entry.txt'],
      'a sheet in a public folder is reported as a leak')

print('%d failure(s)' % len(failures))
sys.exit(1 if failures else 0)
