// The home page's plan preview (handoff_ui.md 8.2): the same GET /plans data as the plan page,
// so prices can never disagree. Only selection and formatting happen here.
import type { Plan, PlansResponse } from './types';

// At most `limit` plans; featured plans are picked first, then the rest in the shop's order. The
// picked plans keep the shop's order, so the preview reads like the plan page.
export function previewPlans(plans: Plan[], limit = 3): Plan[] {
  const picked = new Set(
    [...plans.filter((p) => p.featured), ...plans.filter((p) => !p.featured)]
      .slice(0, limit)
      .map((p) => p.id),
  );
  return plans.filter((p) => picked.has(p.id));
}

// "年付最高省 X%": the server's figure, else the best yearly discount; null without any saving.
export function yearlySavingPercent(response: PlansResponse): number | null {
  const tiers = response.plans.flatMap((p) => (p.yearly ? [p.yearly] : []));
  if (tiers.length === 0 || !tiers.some((t) => t.saving_cents > 0)) return null;
  const best = Math.max(...tiers.map((t) => 100 - t.discount_percent));
  const percent =
    response.max_yearly_saving_percent && response.max_yearly_saving_percent > 0
      ? response.max_yearly_saving_percent
      : best;
  return percent > 0 ? percent : null;
}
