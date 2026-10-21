import type { DeviceStatus, UsageHistory, UsageInterval } from './types';

// Shape checks for the presence and history responses: an unexpected body (an older
// backend, a proxy page) becomes a card-level error instead of breaking the dashboard.
const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null;

export function isDeviceStatus(value: unknown): value is DeviceStatus {
  return (
    isRecord(value) &&
    typeof value.server_time === 'string' &&
    typeof value.device_limit === 'number' &&
    typeof value.online_count === 'number' &&
    Array.isArray(value.online) &&
    Array.isArray(value.signed_in)
  );
}

export function isUsageHistory(value: unknown): value is UsageHistory {
  return (
    isRecord(value) &&
    Array.isArray(value.buckets) &&
    value.buckets.every(
      (bucket) =>
        isRecord(bucket) &&
        typeof bucket.start === 'string' &&
        typeof bucket.end === 'string' &&
        typeof bucket.upload_bytes === 'number' &&
        typeof bucket.download_bytes === 'number' &&
        typeof bucket.charged_units === 'number',
    ) &&
    typeof value.total_upload_bytes === 'number' &&
    typeof value.total_download_bytes === 'number'
  );
}

// Deployment-time setting (DASHBOARD_REFRESH_MS): only this non-secret interval reaches the
// browser, read the same way as NODE_LIST_REFRESH_MS.
export function dashboardRefreshMs(value: string | undefined): number {
  const interval = Number(value ?? 15000);
  return Number.isInteger(interval) && interval >= 5000 && interval <= 120000 ? interval : 15000;
}

const MiB = 1024 ** 2;
const GiB = 1024 ** 3;
const amount = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 });

// Byte counts in the site's binary units (1 GiB = 1,073,741,824 bytes), labelled MB/GB.
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 MB';
  if (bytes >= GiB) return `${amount.format(bytes / GiB)} GB`;
  if (bytes < MiB / 100) return '<0.01 MB';
  return `${amount.format(bytes / MiB)} MB`;
}

// Charged units are bytes × rate permille, the same scale as subscription.used_units.
export const chargedBytes = (units: number) => units / 1000;

function clock(value: string, timeZone?: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
    timeZone,
  }).format(new Date(value));
}

// "09:05–12:05" in the browser's time zone (or the given one); the newest interval runs
// "至现在" because it holds the 5-minute bucket in progress.
export function intervalLabel(
  interval: Pick<UsageInterval, 'start' | 'end'>,
  newest: boolean,
  timeZone?: string,
): { from: string; to: string; text: string } {
  const from = clock(interval.start, timeZone);
  const to = newest ? '至现在' : clock(interval.end, timeZone);
  return { from, to, text: newest ? `${from} 至现在` : `${from}–${to}` };
}

export const intervalTotal = (interval: UsageInterval) =>
  interval.upload_bytes + interval.download_bytes;

// The bar scale: the busiest interval, never zero, so empty intervals draw no bar.
export function chartPeak(intervals: UsageInterval[]): number {
  return Math.max(1, ...intervals.map(intervalTotal));
}

export const historyEmpty = (intervals: UsageInterval[]) =>
  intervals.every((interval) => intervalTotal(interval) === 0);

// Elapsed time between two server timestamps, so a skewed browser clock does not matter.
export function elapsedSeconds(since: string, now: string): number {
  return Math.max(0, Math.round((new Date(now).getTime() - new Date(since).getTime()) / 1000));
}

export function ago(seconds: number): string {
  if (seconds < 60) return `${seconds} 秒前`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`;
  return `${Math.floor(seconds / 3600)} 小时前`;
}

export function duration(seconds: number): string {
  if (seconds < 60) return '不到 1 分钟';
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟`;
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return minutes ? `${hours} 小时 ${minutes} 分钟` : `${hours} 小时`;
}

const platforms: Record<string, string> = {
  macos: 'macOS',
  windows: 'Windows',
  android: 'Android',
  ios: 'iOS',
  linux: 'Linux',
};
export const platformName = (platform: string) => platforms[platform] ?? platform;
