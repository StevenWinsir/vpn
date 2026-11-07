import 'server-only';
import { readAPIConfig } from './api-config';
import {
  createCachedSource,
  fetchPublicJSON,
  publicTTL,
  sanitizeMeta,
  sanitizePlans,
} from '../public-source';
import type { Meta, PlansResponse } from '../types';

// One cache per Next.js process (handoff_seo.md D8): the home page's plan preview and /plans put
// the plans into their first HTML from here; the browser still asks for them after loading, as
// before. The browser acceptance fixture (scripts/test-payment-web.py, E2E_ISOLATED=private-postgres)
// may shorten the minute with PLANS_CACHE_MS, so a plan changed in the console shows at once.
function cacheTTL(): number {
  if (process.env.E2E_ISOLATED !== 'private-postgres') return publicTTL;
  const value = Number(process.env.PLANS_CACHE_MS);
  return Number.isInteger(value) && value >= 500 && value <= publicTTL ? value : publicTTL;
}

const plans = createCachedSource<PlansResponse>({
  ttl: cacheTTL(),
  fetchJSON: () => fetchPublicJSON(fetch, readAPIConfig().internalURL, '/plans'),
  parse: sanitizePlans,
});

const meta = createCachedSource<Meta>({
  ttl: cacheTTL(),
  fetchJSON: () => fetchPublicJSON(fetch, readAPIConfig().internalURL, '/meta'),
  parse: sanitizeMeta,
});

/** The plans on sale, or null when they could not be read (the page then loads them itself). */
export function loadPlans(): Promise<PlansResponse | null> {
  return plans.get();
}

/** The plans with the public capabilities the plan page needs for its buttons, or null. */
export async function loadShop(): Promise<{ plans: PlansResponse; meta: Meta } | null> {
  const [p, m] = await Promise.all([plans.get(), meta.get()]);
  return p && m ? { plans: p, meta: m } : null;
}

/** The public capabilities alone (the home page's answers to common questions), or null. */
export function loadMeta(): Promise<Meta | null> {
  return meta.get();
}
