import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  ago,
  chargedBytes,
  chartPeak,
  dashboardRefreshMs,
  duration,
  elapsedSeconds,
  formatBytes,
  historyEmpty,
  intervalLabel,
  platformName,
} from '../src/lib/dashboard.ts';

test('dashboard refresh configuration is bounded to 5–120 seconds and falls back to 15', () => {
  for (const value of [undefined, '', '0', '-1', '4999', '120001', 'NaN', 'Infinity', '5000.5']) {
    assert.equal(dashboardRefreshMs(value), 15000);
  }
  for (const value of ['5000', '15000', '60000', '120000']) {
    assert.equal(dashboardRefreshMs(value), Number(value));
  }
});

test('byte units follow the site rule 1 GiB = 1,073,741,824 bytes', () => {
  assert.equal(formatBytes(0), '0 MB');
  assert.equal(formatBytes(-5), '0 MB');
  assert.equal(formatBytes(Number.NaN), '0 MB');
  assert.equal(formatBytes(1), '<0.01 MB');
  assert.equal(formatBytes(1024 ** 2), '1 MB');
  assert.equal(formatBytes(1.5 * 1024 ** 2), '1.5 MB');
  assert.equal(formatBytes(1023 * 1024 ** 2), '1,023 MB');
  assert.equal(formatBytes(1024 ** 3), '1 GB');
  assert.equal(formatBytes(1073741824 * 2.345), '2.35 GB');
  // Charged units are bytes × rate permille, the same scale as used_units.
  assert.equal(chargedBytes(1024 ** 3 * 500), 1024 ** 3 / 2);
});

test('interval labels convert UTC boundaries to the viewer time zone', () => {
  const interval = { start: '2026-10-21T01:05:00Z', end: '2026-10-21T04:05:00Z' };
  assert.deepEqual(intervalLabel(interval, false, 'Asia/Shanghai'), {
    from: '09:05',
    to: '12:05',
    text: '09:05–12:05',
  });
  assert.equal(intervalLabel(interval, false, 'UTC').text, '01:05–04:05');
  // Half-hour offsets and the day boundary.
  assert.equal(intervalLabel(interval, false, 'Asia/Kolkata').text, '06:35–09:35');
  assert.equal(intervalLabel(interval, false, 'America/Los_Angeles').text, '18:05–21:05');
  const midnight = { start: '2026-10-21T15:05:00Z', end: '2026-10-21T18:05:00Z' };
  assert.equal(intervalLabel(midnight, false, 'Asia/Shanghai').text, '23:05–02:05');
  assert.deepEqual(intervalLabel(interval, true, 'Asia/Shanghai'), {
    from: '09:05',
    to: '至现在',
    text: '09:05 至现在',
  });
});

test('empty history and chart scale', () => {
  const zero = { start: '', end: '', upload_bytes: 0, download_bytes: 0, charged_units: 0 };
  assert.equal(historyEmpty([]), true);
  assert.equal(historyEmpty(Array(8).fill(zero)), true);
  assert.equal(chartPeak(Array(8).fill(zero)), 1);
  const busy = [zero, { ...zero, upload_bytes: 3, download_bytes: 7 }, { ...zero, download_bytes: 4 }];
  assert.equal(historyEmpty(busy), false);
  assert.equal(chartPeak(busy), 10);
});

test('elapsed time uses server timestamps and reads naturally', () => {
  const now = '2026-10-21T10:00:00Z';
  assert.equal(elapsedSeconds('2026-10-21T09:59:40Z', now), 20);
  assert.equal(elapsedSeconds('2026-10-21T10:00:05Z', now), 0);
  assert.equal(ago(0), '0 秒前');
  assert.equal(ago(59), '59 秒前');
  assert.equal(ago(60), '1 分钟前');
  assert.equal(ago(7200), '2 小时前');
  assert.equal(duration(30), '不到 1 分钟');
  assert.equal(duration(12 * 60), '12 分钟');
  assert.equal(duration(3600), '1 小时');
  assert.equal(duration(3 * 3600 + 5 * 60), '3 小时 5 分钟');
  assert.equal(platformName('macos'), 'macOS');
  assert.equal(platformName('ios'), 'iOS');
  assert.equal(platformName('other'), 'other');
});
