// The plan list and the public capabilities as the website's own server reads them for the first
// HTML (handoff_seo.md D8, 5.3): the same cache-and-fallback shape as the download list
// (download-source.ts), written against an injected fetch and clock so test:seo drives it without
// a server. Only server code imports it (src/lib/server/shop.ts). Every answer is rebuilt field by
// field, so a future backend that says more cannot make the page say more.
import type { Meta, Plan, PlansResponse, PlanTier } from './types.ts';

export const publicTimeoutMs = 3000;
export const publicStaleForMs = 24 * 60 * 60 * 1000;
export const publicTTL = 60_000;

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

/**
 * One public answer, cached for ttl. A failed read keeps serving the last good copy for up to a
 * day; without one the result is null and the page keeps its browser-side loading. A failure is
 * remembered for a few seconds so a dead backend is not asked on every page view.
 */
export function createCachedSource<T>({
  fetchJSON,
  parse,
  now = Date.now,
  ttl = publicTTL,
}: {
  fetchJSON: () => Promise<unknown>;
  parse: (raw: unknown) => T | null;
  now?: () => number;
  ttl?: number;
}) {
  let good: { at: number; value: T } | null = null;
  let failedAt: number | null = null;
  let pending: Promise<T | null> | null = null;
  const retryAfter = Math.min(ttl, 5000);

  const fallback = (): T | null => (good && now() - good.at < publicStaleForMs ? good.value : null);

  const refresh = async (): Promise<T | null> => {
    try {
      const value = parse(await fetchJSON());
      if (value === null) throw new Error('unexpected answer');
      good = { at: now(), value };
      failedAt = null;
      return value;
    } catch {
      failedAt = now();
      return fallback();
    }
  };

  return {
    async get(): Promise<T | null> {
      if (good && now() - good.at < ttl) return good.value;
      if (failedAt !== null && now() - failedAt < retryAfter) return fallback();
      pending ??= refresh().finally(() => {
        pending = null;
      });
      return pending;
    },
  };
}

/** GET {api}{path} with a short timeout; the parsed JSON, or a throw. */
export async function fetchPublicJSON(fetchImpl: Fetch, api: URL, path: string): Promise<unknown> {
  const response = await fetchImpl(`${api.toString().replace(/\/+$/, '')}${path}`, {
    headers: { accept: 'application/json' },
    cache: 'no-store',
    redirect: 'error',
    signal: AbortSignal.timeout(publicTimeoutMs),
  });
  if (!response.ok) {
    await response.body?.cancel();
    throw new Error(`${path} ${response.status}`);
  }
  return response.json();
}

const count = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const percent = (value: unknown): value is number => count(value) && value <= 100;
const text = (value: unknown, max: number): string | null =>
  typeof value === 'string' && value.length <= max ? value : null;
const ID = /^[A-Za-z0-9_-]{1,64}$/;

function tier(raw: unknown): PlanTier | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const fields = [
    'price_cents',
    'original_cents',
    'saving_cents',
    'duration_days',
    'traffic_bytes',
    'monthly_equivalent_cents',
  ] as const;
  if (!fields.every((f) => count(r[f])) || !percent(r.discount_percent)) return null;
  const out: PlanTier = {
    price_cents: r.price_cents as number,
    original_cents: r.original_cents as number,
    saving_cents: r.saving_cents as number,
    discount_percent: r.discount_percent as number,
    duration_days: r.duration_days as number,
    traffic_bytes: r.traffic_bytes as number,
    monthly_equivalent_cents: r.monthly_equivalent_cents as number,
  };
  if (count(r.quota_cycle_days) && count(r.quota_cycle_bytes)) {
    out.quota_cycle_days = r.quota_cycle_days;
    out.quota_cycle_bytes = r.quota_cycle_bytes;
  }
  return out;
}

function plan(raw: unknown): Plan | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const id = text(r.id, 64);
  const name = text(r.name, 200);
  const description = text(r.description ?? '', 2000);
  if (!id || !ID.test(id) || !name || description === null) return null;
  if (![r.price_cents, r.duration_days, r.traffic_bytes, r.max_devices].every(count)) return null;
  if (typeof r.allow_dedicated !== 'boolean') return null;
  const out: Plan = {
    id,
    name,
    description,
    price_cents: r.price_cents as number,
    duration_days: r.duration_days as number,
    traffic_bytes: r.traffic_bytes as number,
    max_devices: r.max_devices as number,
    allow_dedicated: r.allow_dedicated,
  };
  if (typeof r.featured === 'boolean') out.featured = r.featured;
  // Absent (an older backend) and null (no yearly tier) mean different things to the page.
  if ('monthly' in r) {
    const monthly = tier(r.monthly);
    if (!monthly) return null;
    out.monthly = monthly;
  }
  if ('yearly' in r) {
    if (r.yearly === null) out.yearly = null;
    else {
      const yearly = tier(r.yearly);
      if (!yearly) return null;
      out.yearly = yearly;
    }
  }
  return out;
}

/** GET /plans, rebuilt; a plan that does not have the expected shape is left out. */
export function sanitizePlans(raw: unknown): PlansResponse | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  if (!Array.isArray(r.plans)) return null;
  const seen = new Set<string>();
  const plans: Plan[] = [];
  for (const item of r.plans.slice(0, 50)) {
    const p = plan(item);
    if (!p || seen.has(p.id)) continue;
    seen.add(p.id);
    plans.push(p);
  }
  const out = { plans } as PlansResponse;
  // Absent on a backend from before the yearly tier; the page then shows monthly terms only.
  if (percent(r.yearly_discount_percent)) out.yearly_discount_percent = r.yearly_discount_percent;
  if (percent(r.max_yearly_saving_percent))
    out.max_yearly_saving_percent = r.max_yearly_saving_percent;
  return out;
}

const flags = [
  'payment_enabled',
  'allow_registration',
  'password_reset_enabled',
  'referral_enabled',
  'tickets_enabled',
  'plan_upgrade_supported',
] as const;
const seconds = [
  'payment_pending_seconds',
  'verification_code_ttl_seconds',
  'verification_resend_after_seconds',
] as const;

/** GET /meta, rebuilt from the fields the website knows. */
export function sanitizeMeta(raw: unknown): Meta | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  if (typeof r.test_purchase_enabled !== 'boolean' || typeof r.proxy_service_ready !== 'boolean')
    return null;
  const out: Meta = {
    test_purchase_enabled: r.test_purchase_enabled,
    proxy_service_ready: r.proxy_service_ready,
    currency: text(r.currency, 16) ?? '',
    traffic_unit: text(r.traffic_unit, 16) ?? '',
  };
  for (const key of flags) if (typeof r[key] === 'boolean') out[key] = r[key];
  for (const key of seconds) if (count(r[key])) out[key] = r[key];
  const list = (value: unknown, max: number) =>
    Array.isArray(value) && value.length <= max && value.every((v) => text(v, 253) !== null)
      ? (value as string[])
      : undefined;
  const methods = list(r.payment_methods, 10);
  if (methods) out.payment_methods = methods;
  const domains = list(r.registration_email_domains, 100);
  if (domains) out.registration_email_domains = domains;
  return out;
}
