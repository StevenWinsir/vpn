// Integer-only helpers of the plan console (handoff_shop.md 3 D3, 5.2, 6.3). Money is parsed
// from the typed text straight into whole cents and never goes through a float. The yearly
// preview applies the server's rules; the value the server returns after a save is the truth.
import type { AdminPlan } from './console';

export const MAX_MONTHLY_CENTS = 10_000_000; // ¥100000.00
export const MAX_GIB = 1_048_576; // 2^50 bytes
const BYTES_PER_GIB = 1_073_741_824;
export const MAX_PLAN_BYTES = MAX_GIB * BYTES_PER_GIB;

// "39.9" -> 3990, "39.90" -> 3990, "39" -> 3900. Returns null for anything that is not a
// non-negative amount with at most two decimals (no sign, no exponent, no float).
export function yuanToCents(text: string): number | null {
  const m = /^(\d{1,7})(?:\.(\d{1,2}))?$/.exec(text.trim());
  if (!m) return null;
  return Number(m[1]) * 100 + Number((m[2] ?? '').padEnd(2, '0') || '0');
}

export function centsToYuanText(cents: number): string {
  const whole = Math.floor(cents / 100);
  const rest = cents % 100;
  return rest === 0 ? String(whole) : `${whole}.${String(rest).padStart(2, '0')}`;
}

export const integerText = (text: string) => /^\d{1,9}$/.test(text.trim());

export const gibToBytesInt = (gib: number) => gib * BYTES_PER_GIB;
export const bytesToGibText = (bytes: number) =>
  bytes % BYTES_PER_GIB === 0 ? String(bytes / BYTES_PER_GIB) : (bytes / BYTES_PER_GIB).toFixed(2);

export type YearlyPreview = {
  original: number;
  price: number;
  saving: number;
  monthlyEquivalent: number;
};

// original = monthly x 12; price = floor(original x pct / 100); equivalent = floor(price / 12).
export function previewYearly(monthlyCents: number, percent: number): YearlyPreview {
  const original = monthlyCents * 12;
  const price = Math.floor((original * percent) / 100);
  return {
    original,
    price,
    saving: original - price,
    monthlyEquivalent: Math.floor(price / 12),
  };
}

// 90 -> "9 折", 85 -> "8.5 折", 100 -> "不打折".
export function foldText(percent: number): string {
  if (percent >= 100) return '不打折';
  return `${percent / 10} 折`;
}

// Options for plan pickers fed by GET /admin/console/plans?include_deleted=true. A deleted plan
// is not offered, but one already saved in `keep` stays visible by name.
export function planOptions(plans: AdminPlan[], keep: readonly string[] = []) {
  const options = plans
    .filter((p) => !p.deleted_at)
    .map((p) => ({ value: p.id, label: p.active ? p.name : `${p.name}（已下架）` }));
  for (const id of keep) {
    if (!id || options.some((o) => o.value === id)) continue;
    const gone = plans.find((p) => p.id === id);
    options.push({ value: id, label: gone ? `${gone.name}（已删除）` : id });
  }
  return options;
}

export function planNameOf(plans: AdminPlan[], id: string): string {
  const p = plans.find((x) => x.id === id);
  if (!p) return id;
  return p.deleted_at ? `${p.name}（已删除）` : p.active ? p.name : `${p.name}（已下架）`;
}
