import type { ReferralItem, ReferralPage, ReferralStats, ReferralSummary } from './types';

// The field is never validated here: the backend decides silently whether a code is good
// (handoff_coupon.md 12.1). Typing, pasting and ?ref= only get the same normalisation the
// backend applies: no spaces or hyphens, upper case.
export const referralInputMax = 16;
export function normalizeReferralInput(value: string): string {
  return value.replace(/[\s-]/g, '').toUpperCase().slice(0, referralInputMax);
}

// The body field is sent only when the backend announced the programme and the field holds
// something: an older backend rejects unknown request fields.
export function referralField(
  enabled: boolean | undefined,
  value: string,
): { referral_code?: string } {
  const code = normalizeReferralInput(value);
  return enabled === true && code ? { referral_code: code } : {};
}

const forfeitText: Record<string, string> = {
  referrer_no_plan: '发放时无有效套餐',
  referrer_unavailable: '账号不可用',
  zero_amount: '消费金额为 0',
};

export type ReferralBadge = { text: string; color: string };
// "已注册·未消费", "待发放（约 X GB）", "已发放 +X GB", "未生效（原因）"; the amounts are
// formatted by the caller so the units stay the site's own.
export function referralBadge(
  item: ReferralItem,
  format: (bytes: number) => string,
): ReferralBadge {
  switch (item.status) {
    case 'registered':
      return { text: '已注册·未消费', color: 'gray' };
    case 'qualified':
      return { text: `待发放（约 ${format(item.reward_bytes ?? 0)}）`, color: 'blue' };
    case 'rewarded':
      return { text: `已发放 +${format(item.granted_bytes)}`, color: 'teal' };
    case 'forfeited': {
      const reason = forfeitText[item.forfeit_reason];
      return { text: reason ? `未生效（${reason}）` : '未生效', color: 'orange' };
    }
    default:
      return { text: item.status, color: 'gray' };
  }
}

// The reward column: what was or will be granted; nothing for the states without one.
export function referralReward(item: ReferralItem, format: (bytes: number) => string): string {
  if (item.status === 'rewarded') return format(item.granted_bytes);
  if (item.status === 'qualified') return `约 ${format(item.reward_bytes ?? 0)}`;
  return '—';
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null;
const numbers = (value: Record<string, unknown>, keys: string[]) =>
  keys.every((key) => typeof value[key] === 'number');

const statsKeys = [
  'registered_count',
  'purchased_count',
  'pending_count',
  'rewarded_count',
  'rewarded_bytes',
  'forfeited_count',
];
export function isReferralStats(value: unknown): value is ReferralStats {
  return isRecord(value) && numbers(value, statsKeys);
}

// An unexpected body (an older backend, a proxy page) hides the card instead of breaking
// the page.
export function isReferralSummary(value: unknown): value is ReferralSummary {
  return (
    isRecord(value) &&
    typeof value.enabled === 'boolean' &&
    typeof value.code === 'string' &&
    typeof value.share_path === 'string' &&
    typeof value.has_active_plan === 'boolean' &&
    typeof value.current_bonus_bytes === 'number' &&
    isReferralStats(value.stats)
  );
}

export function isReferralPage(value: unknown): value is ReferralPage {
  return (
    isRecord(value) &&
    Array.isArray(value.items) &&
    typeof value.total === 'number' &&
    typeof value.page_size === 'number' &&
    value.items.every(
      (item) =>
        isRecord(item) &&
        typeof item.email_masked === 'string' &&
        typeof item.registered_at === 'string' &&
        typeof item.status === 'string' &&
        typeof item.granted_bytes === 'number',
    )
  );
}
