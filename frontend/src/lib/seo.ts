// Search engine rules of the website (handoff_seo.md 3 and 4): plain functions, no React and no
// server APIs, so node --test checks them (test:seo). The server side (src/lib/server/seo.ts)
// feeds them the environment and the request's host.

type Env = Record<string, string | undefined>;

export type Verification = { google: string | null; bing: string | null; baidu: string | null };

export type SiteConfig = {
  /** The canonical origin, e.g. "https://www.asterlink.vip", or null when SITE_URL is unusable. */
  origin: string | null;
  /** SITE_INDEXING=on and a valid origin (D1). Anything else keeps the whole site out. */
  indexing: boolean;
  verification: Verification;
  /** What was wrong with the environment, without the values themselves (logged once). */
  problems: string[];
};

function loopback(hostname: string): boolean {
  return hostname === 'localhost' || hostname === '[::1]' || /^127(\.\d{1,3}){3}$/.test(hostname);
}

/**
 * SITE_URL accepts what the backend accepts for WEBSITE_BASE_URL: an absolute root origin without
 * user info, query, fragment or path (one trailing slash is tolerated and dropped); HTTPS, or plain
 * HTTP for a loopback host only. The host is lower-cased and a default port dropped.
 */
export function parseSiteURL(raw: string | undefined): string | null {
  const text = raw?.trim() ?? '';
  if (!text || text.includes('?') || text.includes('#') || /\s/.test(text)) return null;
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    return null;
  }
  // The path is "/" for both "https://a.example" and "https://a.example/"; a real path, or a
  // second slash, makes it longer.
  if (url.username || url.password || url.pathname !== '/' || !url.hostname) return null;
  if (url.protocol === 'https:') return `https://${url.host}`;
  if (url.protocol === 'http:' && loopback(url.hostname)) return `http://${url.host}`;
  return null;
}

const TOKEN = /^[A-Za-z0-9_-]{1,100}$/;

function token(name: string, raw: string | undefined, problems: string[]): string | null {
  const text = raw?.trim() ?? '';
  if (!text) return null;
  if (TOKEN.test(text)) return text;
  problems.push(`invalid ${name} (letters, digits, "_" and "-", at most 100); ignored`);
  return null;
}

export function siteConfig(env: Env): SiteConfig {
  const problems: string[] = [];
  const origin = parseSiteURL(env.SITE_URL);
  if (!origin && env.SITE_URL?.trim())
    problems.push(
      'invalid SITE_URL (an origin such as https://www.example.com: HTTPS, no path, query or user info); the site is not indexed',
    );
  const switchValue = env.SITE_INDEXING?.trim().toLowerCase() ?? '';
  if (switchValue !== '' && switchValue !== 'on' && switchValue !== 'off')
    problems.push('invalid SITE_INDEXING (on or off); the site is not indexed');
  const wanted = switchValue === 'on';
  if (wanted && !origin)
    problems.push('SITE_INDEXING=on needs a valid SITE_URL; the site is not indexed');
  return {
    origin,
    indexing: wanted && origin !== null,
    verification: {
      google: token('SITE_VERIFICATION_GOOGLE', env.SITE_VERIFICATION_GOOGLE, problems),
      bing: token('SITE_VERIFICATION_BING', env.SITE_VERIFICATION_BING, problems),
      baidu: token('SITE_VERIFICATION_BAIDU', env.SITE_VERIFICATION_BAIDU, problems),
    },
    problems,
  };
}

/**
 * Whether a request's Host header names the canonical host (D12). Used only to be stricter: a
 * request for any other name (an address, an old domain, a preview host) is never indexed.
 */
export function canonicalHost(origin: string, host: string | null | undefined): boolean {
  const expected = new URL(origin);
  let given = (host ?? '').trim().toLowerCase();
  const defaultPort = expected.protocol === 'https:' ? ':443' : ':80';
  if (given.endsWith(defaultPort)) given = given.slice(0, -defaultPort.length);
  return given !== '' && given === expected.host;
}

/**
 * The page routes of src/app; every page file declares its own (test:seo checks this). The four
 * platform guides share one dynamic page file.
 */
export type Route =
  | '/'
  | '/plans'
  | '/download'
  | '/guide'
  | '/guide/windows'
  | '/guide/macos'
  | '/guide/android'
  | '/guide/ios'
  | '/terms'
  | '/privacy'
  | '/login'
  | '/register'
  | '/forgot-password'
  | '/reset-password'
  | '/dashboard'
  | '/nodes'
  | '/orders'
  | '/referral'
  | '/tickets'
  | '/account'
  | '/pay/result'
  | '/admin/console'
  | '/admin/nodes';

export type Policy = {
  /** May be indexed when the site is (D1, D12). False: noindex whatever the configuration. */
  index: boolean;
  /** Whether links on the page may be followed while it is not indexed. */
  follow: boolean;
  /** Listed in sitemap.xml (D13). */
  sitemap: boolean;
};

const open: Policy = { index: true, follow: true, sitemap: true };
// Navigational brand searches ("AsterLink 登录") land here; the pages are too thin for the sitemap (D11).
const reachable: Policy = { index: true, follow: true, sitemap: false };
// Signed-in pages: never indexed, their links (all to public pages or more signed-in pages) may be followed.
const member: Policy = { index: false, follow: true, sitemap: false };
const closed: Policy = { index: false, follow: false, sitemap: false };

/** handoff_seo.md 4. Anything not listed here (a future page) is not indexed (D4). */
export const policies: Record<Route, Policy> = {
  '/': open,
  '/plans': open,
  '/download': open,
  '/guide': open,
  // One page per platform (handoff_seo.md D5), served by src/app/guide/[platform].
  '/guide/windows': open,
  '/guide/macos': open,
  '/guide/android': open,
  '/guide/ios': open,
  '/terms': open,
  '/privacy': open,
  '/login': reachable,
  '/register': reachable,
  '/forgot-password': closed,
  '/reset-password': closed,
  '/dashboard': member,
  '/nodes': member,
  '/orders': member,
  '/referral': member,
  '/tickets': member,
  '/account': member,
  '/pay/result': member,
  // Every page under /admin/console takes it from the console layout.
  '/admin/console': closed,
  '/admin/nodes': closed,
};

export const sitemapRoutes = (Object.keys(policies) as Route[]).filter((r) => policies[r].sitemap);

/** Paths crawlers are asked to stay out of; the private pages say noindex instead (D10). */
export const disallowedPaths = ['/api/', '/download/get/'];

export type Robots = { index: boolean; follow: boolean };

/** The fixed robots value of a page that is never indexed. */
export function closedRobots(route: Route): Robots {
  const policy = policies[route];
  if (policy.index) throw new Error(`${route} may be indexed; use indexableMetadata`);
  return { index: false, follow: policy.follow };
}

export type PageSEO = { robots: Robots; canonical: string | null };

/**
 * What an indexable page says for one request: indexed only when the site is (D1) and the request
 * came for the canonical host (D12). The canonical address, absolute and without query or
 * fragment (D11), is given whenever indexing is switched on, also to a request for another host.
 */
export function pageSEO(route: Route, config: SiteConfig, host: string | null): PageSEO {
  const policy = policies[route];
  if (!config.indexing || !config.origin)
    return { robots: { index: false, follow: false }, canonical: null };
  const canonical = route === '/' ? `${config.origin}/` : `${config.origin}${route}`;
  const allowed = policy.index && canonicalHost(config.origin, host);
  return {
    robots: allowed ? { index: true, follow: true } : { index: false, follow: false },
    canonical,
  };
}

/** robots.txt (handoff_seo.md 4). */
export function robotsTxt(config: SiteConfig): string {
  if (!config.indexing || !config.origin) return 'User-agent: *\nDisallow: /\n';
  return [
    'User-agent: *',
    'Allow: /',
    ...disallowedPaths.map((path) => `Disallow: ${path}`),
    '',
    `Sitemap: ${config.origin}/sitemap.xml`,
    '',
  ].join('\n');
}

const xml = (value: string) =>
  value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

/** sitemap.xml: the indexable canonical addresses, an empty list while the site is not indexed. */
export function sitemapXml(config: SiteConfig): string {
  const urls =
    config.indexing && config.origin
      ? sitemapRoutes.map((route) => `${config.origin}${route === '/' ? '/' : route}`)
      : [];
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
    ...urls.map((url) => `  <url><loc>${xml(url)}</loc></url>`),
    '</urlset>',
    '',
  ].join('\n');
}
