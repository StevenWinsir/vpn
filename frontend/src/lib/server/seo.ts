import 'server-only';
import type { Metadata } from 'next';
import { headers } from 'next/headers';
import { pageSEO, siteConfig, type Route, type SiteConfig } from '../seo';

// The environment is read on every request (handoff_seo.md D1): changing SITE_URL or
// SITE_INDEXING takes a restart of Next.js, not a rebuild. Each distinct problem is logged once,
// without the offending value. Pages and route handlers load separate copies of this module, so
// what was already said is kept on the process.
const said: Set<string> = ((
  globalThis as { __asterlinkSEOWarnings?: Set<string> }
).__asterlinkSEOWarnings ??= new Set<string>());

function warnOnce(message: string) {
  if (said.has(message)) return;
  said.add(message);
  console.warn(`[seo] ${message}`);
}

export function currentSiteConfig(): SiteConfig {
  const config = siteConfig(process.env);
  for (const problem of config.problems) warnOnce(problem);
  return config;
}

/**
 * The metadata an indexable page adds for one request: robots, canonical and metadataBase. Reading
 * the request's host makes the page render per request, so the values are never frozen into a
 * build (D3).
 */
export async function indexableMetadata(route: Route, extra: Metadata = {}): Promise<Metadata> {
  const config = currentSiteConfig();
  const host = (await headers()).get('host');
  const seo = pageSEO(route, config, host);
  if (config.indexing && !seo.robots.index)
    warnOnce(
      'a page was requested under a host other than SITE_URL and was served noindex; check that the proxy passes the original Host header',
    );
  return {
    ...extra,
    ...(config.origin ? { metadataBase: new URL(config.origin) } : {}),
    robots: seo.robots,
    ...(seo.canonical ? { alternates: { canonical: seo.canonical } } : {}),
  };
}

/** Search engine site verification tags (D15), on the home page only. */
export function verificationMetadata(config: SiteConfig): Metadata {
  const { google, bing, baidu } = config.verification;
  const other: Record<string, string> = {};
  if (bing) other['msvalidate.01'] = bing;
  if (baidu) other['baidu-site-verification'] = baidu;
  if (!google && !bing && !baidu) return {};
  return { verification: { ...(google ? { google } : {}), ...(bing || baidu ? { other } : {}) } };
}
