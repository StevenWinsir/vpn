// Display helpers for the plan shop (handoff_shop.md 8). Every price and discount comes from the
// server; these only format it.
import { gib, type BillingCycle, type Plan, type PlanTier } from './types';

export const cycleName = (cycle: BillingCycle | undefined) =>
  cycle === 'yearly' ? '年付' : '月付';

type Traffic = { traffic_bytes: number; quota_cycle_days?: number; quota_cycle_bytes?: number };

// What a cycle grants in traffic: "100 GiB", or for yearly "每 30 天 100 GiB" (the monthly
// allowance, refreshed every 30 days; handoff_shop.md D2 revised).
export function trafficLabel(t: Traffic): string {
  return t.quota_cycle_days && t.quota_cycle_bytes
    ? `每 ${t.quota_cycle_days} 天 ${gib(t.quota_cycle_bytes)} GiB`
    : `${gib(t.traffic_bytes)} GiB`;
}

export const refreshes = (t: Traffic | null | undefined) =>
  Boolean(t?.quota_cycle_days && t.quota_cycle_bytes);

// 90 -> "9 折", 85 -> "8.5 折".
export function zhe(percent: number): string {
  const tenths = percent / 10;
  return `${Number.isInteger(tenths) ? tenths : tenths.toFixed(1)} 折`;
}

// The tier a card shows for the chosen cycle. Monthly falls back to the top-level fields, which
// is all an older backend sends.
export function tierOf(plan: Plan, cycle: BillingCycle): PlanTier | null {
  if (cycle === 'yearly') return plan.yearly ?? null;
  return (
    plan.monthly ?? {
      price_cents: plan.price_cents,
      original_cents: plan.price_cents,
      saving_cents: 0,
      discount_percent: 100,
      duration_days: plan.duration_days,
      traffic_bytes: plan.traffic_bytes,
      monthly_equivalent_cents: plan.price_cents,
    }
  );
}

export type YearlyPromo = {
  // The badge next to the yearly option; null when there is no discount at all.
  badge: string | null;
  // "全部套餐享 9 折" or "最高省 X%".
  headline: string;
  maxSavingCents: number;
};

export function yearlyPromo(plans: Plan[], maxSavingPercent?: number): YearlyPromo | null {
  const tiers = plans.flatMap((p) => (p.yearly ? [p.yearly] : []));
  if (tiers.length === 0) return null;
  const discounts = [...new Set(tiers.map((t) => t.discount_percent))];
  const maxSavingCents = Math.max(...tiers.map((t) => t.saving_cents));
  const best = Math.max(...discounts.map((d) => 100 - d));
  const percent = maxSavingPercent && maxSavingPercent > 0 ? maxSavingPercent : best;
  if (percent <= 0 || maxSavingCents <= 0) return { badge: null, headline: '', maxSavingCents: 0 };
  const same = discounts.length === 1;
  const everyPlan = tiers.length === plans.length;
  return {
    badge: same ? zhe(discounts[0]) : `最高省 ${percent}%`,
    headline: same
      ? `${everyPlan ? '全部套餐' : '年付套餐'}享 ${zhe(discounts[0])}`
      : `最高省 ${percent}%`,
    maxSavingCents,
  };
}
