import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import {
  archLabels,
  firstFree,
  formatLabels,
  normalizeHex,
  parseSize,
  platformLabels,
  takenVariants,
  toInput,
  validateDownload,
} from '../src/lib/download-catalog.ts';

// The backend's catalogue snapshot (handoff_download.md 4.2): TestClientDownloadCatalogSnapshot
// keeps it equal to the Go constants, this file keeps the console's names and checks in step.
const catalog = JSON.parse(
  readFileSync(
    new URL('../../backend/internal/api/testdata/client_downloads_catalog.json', import.meta.url),
    'utf8',
  ),
);

const sha = 'ab'.repeat(32);
const base = (platform, arch, format) => {
  const store = format in catalog.store_hosts;
  return {
    platform,
    arch,
    format,
    app_version: '1.2.3',
    url: store
      ? `https://${catalog.store_hosts[format]}/app/asterlink/id123`
      : `https://downloads.example.test/AsterLink-1.2.3${catalog.suffixes[format]}`,
    sha256: store ? '' : sha,
    size_bytes: store ? null : 1024,
    signing_cert_sha256: '',
    bundle_id: format === 'ipa' ? 'com.example.asterlink' : '',
    published: false,
  };
};

test('every platform, architecture and format of the catalogue has a name, in page order', () => {
  assert.deepEqual(
    catalog.platforms.map((p) => p.platform),
    ['windows', 'macos', 'android', 'ios', 'linux'],
  );
  for (const p of catalog.platforms) {
    assert.ok(platformLabels[p.platform], p.platform);
    for (const arch of p.archs) assert.ok(archLabels[arch], arch);
    for (const format of p.formats) {
      assert.ok(formatLabels[format], format);
      if (!(format in catalog.store_hosts)) assert.ok(catalog.suffixes[format], format);
    }
  }
});

test('every catalogue combination passes, as on the server', () => {
  let n = 0;
  for (const p of catalog.platforms)
    for (const arch of p.archs)
      for (const format of p.formats) {
        assert.equal(validateDownload(catalog, base(p.platform, arch, format)), null);
        n++;
      }
  assert.equal(n, 27);
});

test('the same field errors as the server, first failure wins', () => {
  const win = base('windows', 'x64', 'exe');
  const cases = [
    [{ platform: 'beos' }, 'platform'],
    [{ arch: 'arm64-v8a' }, 'arch'],
    [{ format: 'dmg' }, 'format'],
    [{ app_version: 'latest' }, 'app_version'],
    [{ app_version: '1.2' }, 'app_version'],
    [{ app_version: '1.2.3-' + 'x'.repeat(30) }, 'app_version'],
    [{ url: '' }, 'url'],
    [{ url: 'http://downloads.example.test/a.exe' }, 'url'],
    [{ url: 'https://user:pass@downloads.example.test/a.exe' }, 'url'],
    [{ url: 'javascript:alert(1)' }, 'url'],
    [{ url: 'https://downloads.example.test/a.exe#x' }, 'url'],
    [{ url: 'https://downloads.example.test/a.dmg' }, 'url'],
    [{ url: 'https://downloads.example.test/a?f=a.exe' }, 'url'],
    [{ url: 'https://downloads.example.test/' + 'a'.repeat(1020) + '.exe' }, 'url'],
    [{ sha256: 'abc' }, 'sha256'],
    [{ sha256: '' }, 'sha256'],
    [{ size_bytes: 0 }, 'size_bytes'],
    [{ size_bytes: 8 * 2 ** 30 + 1 }, 'size_bytes'],
    [{ signing_cert_sha256: sha }, 'signing_cert_sha256'],
    [{ bundle_id: 'com.example.app' }, 'bundle_id'],
  ];
  for (const [patch, field] of cases)
    assert.equal(
      validateDownload(catalog, { ...win, ...patch })?.field,
      field,
      JSON.stringify(patch),
    );

  const store = base('ios', 'arm64', 'app_store');
  assert.equal(validateDownload(catalog, { ...store, sha256: sha })?.field, 'sha256');
  assert.equal(validateDownload(catalog, { ...store, size_bytes: 1 })?.field, 'size_bytes');
  for (const url of [
    'http://apps.apple.com/app/id1',
    'https://apps.apple.com:8443/app/id1',
    'https://testflight.apple.com/join/abc',
    'https://evil.example/apps.apple.com',
  ])
    assert.equal(validateDownload(catalog, { ...store, url })?.field, 'url', url);

  const ipa = base('ios', 'arm64', 'ipa');
  assert.equal(validateDownload(catalog, { ...ipa, bundle_id: '' })?.field, 'bundle_id');
  assert.equal(validateDownload(catalog, { ...ipa, bundle_id: 'nodot' })?.field, 'bundle_id');
  const apk = base('android', 'arm64-v8a', 'apk');
  assert.equal(
    validateDownload(catalog, { ...apk, signing_cert_sha256: 'zz' })?.field,
    'signing_cert_sha256',
  );
});

test('what the server accepts, the console lets through', () => {
  const win = base('windows', 'x64', 'exe');
  for (const patch of [
    { url: 'https://downloads.example.test/AsterLink-Setup.EXE' },
    { url: 'https://downloads.example.test/AsterLink-Setup.exe?token=abc' },
    // Plain http to a loopback address: the server allows it outside production only.
    { url: 'http://127.0.0.1:8080/AsterLink-Setup.exe' },
    { url: 'http://localhost/AsterLink-Setup.exe' },
    { sha256: 'AB:'.repeat(31) + 'AB' },
    { size_bytes: null },
    { app_version: '1.2.3-beta.1' },
  ])
    assert.equal(validateDownload(catalog, { ...win, ...patch }), null, JSON.stringify(patch));
  const linux = base('linux', 'x64', 'appimage');
  assert.equal(
    validateDownload(catalog, { ...linux, url: 'https://d.example.test/AsterLink.AppImage' }),
    null,
  );
  const tgz = base('linux', 'arm64', 'tar.gz');
  assert.equal(validateDownload(catalog, { ...tgz, url: 'https://d.example.test/a.tar.gz' }), null);
  const apk = base('android', 'universal', 'apk');
  assert.equal(
    validateDownload(catalog, { ...apk, signing_cert_sha256: 'F5:EB ' + 'aa'.repeat(30) }),
    null,
  );
});

test('hex is normalised the way the server stores it', () => {
  assert.equal(normalizeHex(' AB:CD ef\t01 '), 'abcdef01');
});

test('sizes are whole bytes up to 8 GiB; empty is no size', () => {
  assert.equal(parseSize(''), null);
  assert.equal(parseSize(' 48128121 '), 48128121);
  assert.equal(parseSize('0'), 'invalid');
  assert.equal(parseSize('1.5'), 'invalid');
  assert.equal(parseSize('-1'), 'invalid');
  assert.equal(parseSize(String(8 * 2 ** 30)), 8 * 2 ** 30);
  assert.equal(parseSize(String(8 * 2 ** 30 + 1)), 'invalid');
});

test('taken combinations are offered no more, except the row being edited', () => {
  const rows = [
    { id: 'a', platform: 'windows', arch: 'x64', format: 'exe' },
    { id: 'b', platform: 'windows', arch: 'x64', format: 'msi' },
    { id: 'c', platform: 'macos', arch: 'arm64', format: 'dmg' },
  ];
  assert.deepEqual([...takenVariants(rows, 'windows')].sort(), ['x64|exe', 'x64|msi']);
  assert.deepEqual([...takenVariants(rows, 'windows', 'a')], ['x64|msi']);
  assert.deepEqual(firstFree(catalog, rows, 'windows'), { arch: 'x64', format: 'zip' });
  assert.deepEqual(firstFree(catalog, rows, 'ios'), { arch: 'arm64', format: 'ipa' });
  const full = catalog.platforms[3].formats.map((format, i) => ({
    id: String(i),
    platform: 'ios',
    arch: 'arm64',
    format,
  }));
  assert.equal(firstFree(catalog, full, 'ios'), null);
});

test('saving a row again sends exactly the keys the API decodes', () => {
  const row = {
    id: 'x',
    platform: 'android',
    arch: 'arm64-v8a',
    format: 'apk',
    app_version: '1.2.3',
    url: 'https://downloads.example.test/a.apk',
    sha256: sha,
    size_bytes: 5,
    signing_cert_sha256: null,
    bundle_id: null,
    published: true,
    released_at: '',
    version: 4,
    created_at: '',
    updated_at: '',
  };
  assert.deepEqual(Object.keys(toInput(row)).sort(), [
    'app_version',
    'arch',
    'bundle_id',
    'format',
    'platform',
    'published',
    'sha256',
    'signing_cert_sha256',
    'size_bytes',
    'url',
    'version',
  ]);
  assert.equal(toInput(row).signing_cert_sha256, '');
  assert.equal(toInput(row).version, 4);
});
