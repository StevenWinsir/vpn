import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  buttonLabel,
  downloadPath,
  grouped,
  guideVersion,
  homeStatus,
  iosInstallLink,
  iosManifest,
  iosManifestPath,
  megabytes,
  platformView,
  sanitizeDownloads,
} from '../src/lib/downloads.ts';
import {
  clientAddress,
  createDownloadSource,
  redirectResponse,
  resolveDownload,
  upstreamHeaders,
} from '../src/lib/download-source.ts';

// The public download pages (handoff_download.md 6, D4, D9, D10, D11): what the backend's list
// becomes on the page, the cache in front of it, the redirect lookup and the iPhone manifest.
const sha = (n) => n.toString(16).padStart(2, '0').repeat(32);
const row = (platform, arch, format, extra = {}) => ({
  platform,
  arch,
  format,
  app_version: '1.2.3',
  size_bytes: 48128121,
  sha256: format === 'app_store' || format === 'testflight' ? null : sha(1),
  signing_cert_sha256: null,
  bundle_id: format === 'ipa' ? 'com.example.asterlink' : null,
  released_at: '2026-11-05T00:00:00Z',
  ...extra,
});

test('only known combinations and known fields survive, in page order', () => {
  const list = sanitizeDownloads({
    downloads: [
      row('linux', 'x64', 'appimage'),
      row('android', 'arm64-v8a', 'apk', {
        url: 'https://downloads.example.test/a.apk',
        id: 'x',
        signing_cert_sha256: sha(0xf5),
      }),
      row('windows', 'x64', 'exe'),
      row('beos', 'x64', 'exe'),
      row('windows', 'x64', 'dmg'),
      row('windows', 'x64', 'exe'),
      row('macos', 'arm64', 'dmg', { app_version: 'latest' }),
      row('ios', 'arm64', 'ipa', { bundle_id: 'no dots' }),
      row('ios', 'arm64', 'app_store', { size_bytes: 5 }),
      row('android', 'x86_64', 'apk', { sha256: 'short' }),
    ],
  });
  assert.deepEqual(
    list.map((e) => `${e.platform}/${e.arch}/${e.format}`),
    ['windows/x64/exe', 'android/arm64-v8a/apk', 'ios/arm64/app_store', 'linux/x64/appimage'],
  );
  const json = JSON.stringify(list);
  assert.ok(!json.includes('downloads.example.test'));
  assert.ok(!json.includes('"id"'));
  assert.equal(list[1].certSha256, sha(0xf5));
  // A store link has no file: no size, no hash.
  assert.equal(list[2].sizeBytes, null);
  assert.equal(list[2].sha256, null);
  assert.equal(sanitizeDownloads({}), null);
  assert.equal(sanitizeDownloads(null), null);
  assert.deepEqual(sanitizeDownloads({ downloads: [] }), []);
});

test('buttons go to the site path, the recommended architecture is filled, versions merge', () => {
  const entries = sanitizeDownloads({
    downloads: [
      row('android', 'arm64-v8a', 'apk'),
      row('android', 'armeabi-v7a', 'apk', { sha256: sha(2), size_bytes: null }),
      row('windows', 'x64', 'exe'),
      row('windows', 'arm64', 'exe', { app_version: '1.2.4', sha256: sha(3) }),
    ],
  });
  const android = platformView(entries, 'android');
  assert.equal(android.badge, '1.2.3');
  assert.deepEqual(
    android.buttons.map((b) => [b.href, b.label, b.solid, b.version]),
    [
      ['/download/get/android/arm64-v8a/apk', '下载 APK（64 位手机 · 45.9 MB）', true, null],
      ['/download/get/android/armeabi-v7a/apk', '下载 APK（32 位旧款手机）', false, null],
    ],
  );
  assert.deepEqual(
    android.files.map((f) => f.title),
    ['SHA-256（arm64-v8a · APK 安装包）', 'SHA-256（armeabi-v7a · APK 安装包）'],
  );
  const windows = platformView(entries, 'windows');
  assert.equal(windows.badge, null);
  assert.deepEqual(
    windows.buttons.map((b) => [b.label, b.solid, b.version]),
    [
      ['下载安装程序（x64 · 45.9 MB）', true, '1.2.3'],
      ['下载安装程序（ARM64 · 45.9 MB）', false, '1.2.4'],
    ],
  );
  const none = platformView(entries, 'linux');
  assert.equal(none.buttons.length, 0);
  assert.equal(none.badge, null);
});

test('a single Android file reads as before; desktop and store buttons say what they are', () => {
  const [apk] = sanitizeDownloads({ downloads: [row('android', 'arm64-v8a', 'apk')] });
  assert.equal(buttonLabel(apk, false), '下载 APK（45.9 MB）');
  const view = platformView([apk], 'android');
  assert.equal(view.files[0].title, 'SHA-256（下载后可核对文件完整性）');
  const entries = sanitizeDownloads({
    downloads: [
      row('macos', 'arm64', 'dmg'),
      row('ios', 'arm64', 'ipa'),
      row('ios', 'arm64', 'testflight'),
      row('linux', 'x64', 'deb', { size_bytes: null }),
    ],
  });
  assert.equal(platformView(entries, 'macos').buttons[0].label, '下载 DMG（Apple 芯片 · 45.9 MB）');
  const ios = platformView(entries, 'ios');
  assert.deepEqual(
    ios.buttons.map((b) => [b.label, b.ios, b.href]),
    [
      ['在 iPhone 上安装（45.9 MB）', true, iosManifestPath],
      ['通过 TestFlight 安装', false, '/download/get/ios/arm64/testflight'],
    ],
  );
  assert.equal(ios.hasIpa, true);
  assert.equal(ios.store, 'testflight');
  assert.equal(ios.files[0].title, 'SHA-256（IPA 文件，下载后可核对文件完整性）');
  assert.equal(platformView(entries, 'linux').buttons[0].label, '下载 DEB（x64）');
});

test('the iPhone install link names only this site’s manifest route', () => {
  const link = iosInstallLink('https://www.example.test');
  assert.equal(
    link,
    'itms-services://?action=download-manifest&url=https%3A%2F%2Fwww.example.test%2Fdownload%2Fget%2Fios%2Farm64%2Fipa%2Fmanifest.plist',
  );
  assert.equal(
    downloadPath({ platform: 'linux', arch: 'x64', format: 'tar.gz' }),
    '/download/get/linux/x64/tar.gz',
  );
});

test('the home cards and the guide follow the list; an unreadable list is not "coming soon"', () => {
  const entries = sanitizeDownloads({
    downloads: [
      row('android', 'armeabi-v7a', 'apk', { app_version: '1.0.0' }),
      row('android', 'arm64-v8a', 'apk'),
    ],
  });
  assert.equal(homeStatus({ status: 'ok', entries }, 'android'), 'ready');
  assert.equal(homeStatus({ status: 'ok', entries }, 'windows'), 'soon');
  assert.equal(homeStatus({ status: 'unavailable', entries: [] }, 'android'), 'unknown');
  assert.equal(guideVersion(entries, 'android'), '1.2.3');
  assert.equal(
    guideVersion(
      entries.filter((e) => e.arch === 'armeabi-v7a'),
      'android',
    ),
    '1.0.0',
  );
  assert.equal(guideVersion(entries, 'ios'), null);
});

test('the manifest is a property list with every value escaped', () => {
  const plist = iosManifest({
    ipaUrl: 'https://d.example.test/a.ipa?x=1&y=<2>',
    bundleId: 'com.example.app',
    version: '1.2.3-beta.1',
  });
  assert.ok(plist.startsWith('<?xml version="1.0" encoding="UTF-8"?>'));
  assert.ok(plist.includes('<string>https://d.example.test/a.ipa?x=1&amp;y=&lt;2&gt;</string>'));
  assert.ok(
    plist.includes('<key>bundle-identifier</key>\n        <string>com.example.app</string>'),
  );
  assert.ok(plist.includes('<string>1.2.3-beta.1</string>'));
  assert.ok(plist.includes('<string>software-package</string>'));
  assert.ok(!/&(?!amp;|lt;|gt;|quot;|apos;)/.test(plist));
});

test('the cache serves for its lifetime, keeps the last good copy, and recovers', async () => {
  let clock = 0;
  let calls = 0;
  let answer = { downloads: [row('android', 'arm64-v8a', 'apk')] };
  const source = createDownloadSource({
    ttl: 30_000,
    now: () => clock,
    fetchList: async () => {
      calls++;
      if (answer instanceof Error) throw answer;
      return answer;
    },
  });
  assert.equal((await source.get()).entries.length, 1);
  clock = 29_000;
  await source.get();
  assert.equal(calls, 1, 'within 30 seconds the list is not asked again');
  // Two pages at once share one request.
  clock = 31_000;
  await Promise.all([source.get(), source.get()]);
  assert.equal(calls, 2);
  // The backend stops answering: the last good copy stays, for up to a day.
  answer = new Error('down');
  clock = 70_000;
  let state = await source.get();
  assert.deepEqual([state.status, state.entries.length], ['ok', 1]);
  clock = 71_000;
  await source.get();
  assert.equal(calls, 3, 'a failure is not retried on every request');
  clock = 31_000 + 24 * 3600 * 1000 + 1;
  state = await source.get();
  assert.deepEqual([state.status, state.entries.length], ['unavailable', 0]);
  // It comes back: the next request after the pause reads the new list.
  answer = { downloads: [row('android', 'arm64-v8a', 'apk', { app_version: '1.2.4' })] };
  clock += 6000;
  state = await source.get();
  assert.deepEqual([state.status, state.entries[0].version], ['ok', '1.2.4']);
});

test('a backend that never answered leaves the page saying so; a wrong shape is a failure', async () => {
  const source = createDownloadSource({ now: () => 0, fetchList: async () => ({ nope: true }) });
  assert.deepEqual(await source.get(), { status: 'unavailable', entries: [] });
});

const api = new URL('http://127.0.0.1:9/api/v1');
const upstream = (status, location) => async (url, init) => {
  upstream.last = { url, init };
  return new Response(null, { status, headers: location ? { location } : {} });
};

test('the redirect lookup asks the backend, checks the address again, and echoes nothing else', async () => {
  const headers = new Headers();
  const ok = await resolveDownload(
    upstream(302, 'https://downloads.example.test/AsterLink-Setup-x64.exe?token=a'),
    api,
    { platform: 'windows', arch: 'x64', format: 'exe' },
    headers,
  );
  assert.equal(ok, 'https://downloads.example.test/AsterLink-Setup-x64.exe?token=a');
  assert.equal(upstream.last.url, 'http://127.0.0.1:9/api/v1/downloads/windows/x64/exe');
  assert.equal(upstream.last.init.redirect, 'manual');
  const variant = { platform: 'windows', arch: 'x64', format: 'exe' };
  for (const [status, location] of [
    [302, 'javascript:alert(1)'],
    [302, 'http://downloads.example.test/a.exe'],
    [302, 'https://downloads.example.test/a.dmg'],
    [302, 'https://user:pw@downloads.example.test/a.exe'],
    [404, null],
    [200, null],
    [301, 'https://downloads.example.test/a.exe'],
  ])
    assert.equal(await resolveDownload(upstream(status, location), api, variant, headers), null);
  let asked = false;
  const never = async () => {
    asked = true;
    return new Response(null, { status: 302, headers: { location: 'https://x.test/a.exe' } });
  };
  for (const v of [
    { platform: 'windows', arch: 'x64', format: 'dmg' },
    { platform: 'beos', arch: 'x64', format: 'exe' },
    { platform: 'windows', arch: '../admin', format: 'exe' },
  ])
    assert.equal(await resolveDownload(never, api, v, headers), null);
  assert.equal(asked, false, 'an unknown combination never reaches the backend');
  const broken = async () => {
    throw new Error('timeout');
  };
  assert.equal(await resolveDownload(broken, api, variant, headers), null);
  assert.equal(
    await resolveDownload(
      upstream(302, 'https://apps.apple.com/app/asterlink/id1'),
      api,
      { platform: 'ios', arch: 'arm64', format: 'app_store' },
      headers,
    ),
    'https://apps.apple.com/app/asterlink/id1',
  );
});

test('the browser address goes upstream only from the trusted header, and only one IP', () => {
  const request = (value) =>
    new Request('http://site.test/x', { headers: value ? { 'x-real-ip': value } : {} });
  assert.equal(
    upstreamHeaders(request('203.0.113.7'), 'x-real-ip').get('x-forwarded-for'),
    '203.0.113.7',
  );
  assert.equal(upstreamHeaders(request('203.0.113.7'), '').get('x-forwarded-for'), null);
  assert.equal(clientAddress('198.51.100.1, 203.0.113.9'), '203.0.113.9');
  assert.equal(clientAddress('not-an-ip'), null);
  assert.equal(clientAddress(null), null);
  const forwarded = upstreamHeaders(
    new Request('http://site.test/x', { headers: { cookie: 'a=b', authorization: 'x' } }),
    '',
  );
  assert.equal(forwarded.get('cookie'), null);
  assert.equal(forwarded.get('authorization'), null);
});

test('redirects are never cached, indexed or sent with a referrer', () => {
  const response = redirectResponse('/download#android');
  assert.equal(response.status, 302);
  assert.equal(response.headers.get('location'), '/download#android');
  assert.equal(response.headers.get('cache-control'), 'no-store');
  assert.equal(response.headers.get('referrer-policy'), 'no-referrer');
  assert.equal(response.headers.get('x-robots-tag'), 'noindex');
});

test('formatting', () => {
  assert.equal(megabytes(48128121), '45.9 MB');
  assert.equal(grouped('abcd1234'), 'abcd 1234');
});
