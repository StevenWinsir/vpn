import assert from 'node:assert/strict';
import { test } from 'node:test';
import { androidDownload, grouped, megabytes } from '../src/lib/downloads.ts';

const good = {
  ANDROID_APK_URL: 'https://test.hyshentou.cn/downloads/AsterLink-0.1.0-android-arm64-v8a.apk',
  ANDROID_APK_VERSION: '0.1.0',
  ANDROID_APK_SHA256: 'BB155B2FE7F6651C71976E7CD867FCA7FEBD4E0C514D694C21C4D7E9568393A6',
  ANDROID_APK_SIZE_BYTES: '48128121',
  ANDROID_CERT_SHA256:
    'f5:eb:55:6c:08:7d:14:be:e4:51:9a:03:ae:68:a2:6d:ff:11:50:23:69:c1:64:54:d7:47:bb:48:cd:ed:7a:d4',
};

test('a complete release is offered with normalised hashes', () => {
  const download = androidDownload(good);
  assert.equal(download.version, '0.1.0');
  assert.equal(download.sha256, good.ANDROID_APK_SHA256.toLowerCase());
  assert.equal(download.sizeBytes, 48128121);
  assert.equal(
    download.certSha256,
    'f5eb556c087d14bee4519a03ae68a26dff11502369c16454d747bb48cded7ad4',
  );
  assert.equal(download.sourceUrl, null);
});

test('anything missing or unsafe hides the download', () => {
  assert.equal(androidDownload({}), null);
  for (const [key, value] of [
    ['ANDROID_APK_URL', 'http://test.hyshentou.cn/a.apk'],
    ['ANDROID_APK_URL', 'https://user:pw@test.hyshentou.cn/a.apk'],
    ['ANDROID_APK_URL', 'https://test.hyshentou.cn/a.zip'],
    ['ANDROID_APK_URL', 'javascript:alert(1)'],
    ['ANDROID_APK_VERSION', 'latest'],
    ['ANDROID_APK_SHA256', 'abc'],
  ]) {
    assert.equal(androidDownload({ ...good, [key]: value }), null, `${key}=${value}`);
  }
  assert.ok(androidDownload({ ...good, ANDROID_APK_URL: 'http://localhost:3000/a.apk' }));
});

test('optional facts degrade to null instead of hiding the APK', () => {
  const download = androidDownload({
    ...good,
    ANDROID_APK_SIZE_BYTES: '-1',
    ANDROID_CERT_SHA256: 'nope',
    ANDROID_SOURCE_URL: 'ftp://x',
  });
  assert.equal(download.sizeBytes, null);
  assert.equal(download.certSha256, null);
  assert.equal(download.sourceUrl, null);
});

test('formatting', () => {
  assert.equal(megabytes(48128121), '45.9 MB');
  assert.equal(grouped('abcd1234ef'), 'abcd 1234 ef');
});
