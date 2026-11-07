import { expect, test } from '@playwright/test';
import { request as httpRequest } from 'node:http';
import { readFile } from 'node:fs/promises';

// Search engine acceptance, round 1 (handoff_seo.md 9, S1-03 … S1-09, S1-11): plain HTTP, no
// browser page. scripts/test-payment-web.py runs one build twice: PLAYWRIGHT_BASE_URL without the
// switch (never indexed) and SEO_INDEXED_URL with SITE_INDEXING=on and its own address as SITE_URL.
type Fixture = { api: string; control: string };

const closedURL = process.env.PLAYWRIGHT_BASE_URL ?? '';
const indexedURL = process.env.SEO_INDEXED_URL ?? '';
let fixture: Fixture;

test.beforeAll(async () => {
  if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(indexedURL))
    throw new Error('SEO_INDEXED_URL required (scripts/test-payment-web.py)');
  const filename = process.env.PAYMENT_FIXTURE;
  if (!filename?.startsWith('/tmp/vpn-payment-'))
    throw new Error('Private payment fixture required');
  fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
  // One published Android package, so /download has a redirect button to look at.
  const response = await fetch(`${fixture.api}/fixture/downloads?set=base`, {
    method: 'POST',
    headers: { 'X-Acceptance-Control': fixture.control },
  });
  if (response.status !== 204) throw new Error(`fixture downloads: ${response.status}`);
  await new Promise((resolve) => setTimeout(resolve, 1200));
});

type Answer = {
  status: number;
  headers: Record<string, string>;
  head: string;
  body: string;
  html: string;
};

// node:http rather than fetch: the Host header must be settable (S1-08), and redirects are
// never followed.
function get(base: string, path: string, host?: string): Promise<Answer> {
  const url = new URL(path, base);
  return new Promise((resolve, reject) => {
    const req = httpRequest(
      url,
      {
        headers: {
          'User-Agent':
            'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/130.0 Safari/537.36',
          ...(host ? { Host: host } : {}),
        },
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on('data', (chunk: Buffer) => chunks.push(chunk));
        res.on('end', () => {
          const html = Buffer.concat(chunks).toString('utf8');
          const cut = html.indexOf('</head>');
          const headers: Record<string, string> = {};
          for (const [key, value] of Object.entries(res.headers))
            headers[key] = Array.isArray(value) ? value.join(', ') : (value ?? '');
          resolve({
            status: res.statusCode ?? 0,
            headers,
            head: cut < 0 ? '' : html.slice(0, cut),
            body: cut < 0 ? html : html.slice(cut),
            html,
          });
        });
      },
    );
    req.on('error', reject);
    req.setTimeout(30000, () => req.destroy(new Error(`timeout ${url}`)));
    req.end();
  });
}

const robotsOf = (part: string) =>
  [...part.matchAll(/<meta name="robots" content="([^"]*)"\s*\/?>/g)].map((m) => m[1]);
const canonicalOf = (part: string) =>
  [...part.matchAll(/<link rel="canonical" href="([^"]*)"\s*\/?>/g)].map((m) => m[1]);
// "http://h:1" and "http://h:1/" name the same page.
const same = (a: string) => new URL(a).href;

const indexable = ['/', '/plans', '/download', '/guide', '/login', '/register'];
const closedPages: [string, string][] = [
  ['/dashboard', 'noindex, follow'],
  ['/nodes', 'noindex, follow'],
  ['/orders', 'noindex, follow'],
  ['/referral', 'noindex, follow'],
  ['/tickets', 'noindex, follow'],
  ['/account', 'noindex, follow'],
  ['/pay/result', 'noindex, follow'],
  ['/forgot-password', 'noindex, nofollow'],
  ['/reset-password', 'noindex, nofollow'],
  ['/admin/console/users', 'noindex, nofollow'],
  ['/admin/console/downloads', 'noindex, nofollow'],
  ['/admin/nodes', 'noindex, nofollow'],
];
const seen: string[] = [];

test('S1-03: the same build indexes the public pages only with the switch on, tags in <head>', async () => {
  for (const path of indexable) {
    const off = await get(closedURL, path);
    expect(off.status, path).toBe(200);
    expect(robotsOf(off.head), path).toEqual(['noindex, nofollow']);
    expect(canonicalOf(off.html), path).toEqual([]);
    const on = await get(indexedURL, path);
    expect(on.status, path).toBe(200);
    expect(robotsOf(on.head), path).toEqual(['index, follow']);
    expect(canonicalOf(on.head).map(same), path).toEqual([same(indexedURL + path)]);
    // Nothing streamed into the body (next.config htmlLimitedBots).
    expect(robotsOf(on.body), path).toEqual([]);
    expect(canonicalOf(on.body), path).toEqual([]);
    seen.push(off.html, on.html);
  }
});

test('D15: the verification tags are on the home page of the indexed process only', async () => {
  const home = await get(indexedURL, '/');
  expect(home.head).toContain(
    '<meta name="google-site-verification" content="fixture-google-token"/>',
  );
  expect(home.head).toContain('<meta name="msvalidate.01" content="FIXTUREBINGTOKEN"/>');
  const plans = await get(indexedURL, '/plans');
  expect(plans.head).not.toContain('site-verification');
  expect((await get(closedURL, '/')).head).not.toContain('site-verification');
});

test('S1-04: robots.txt and sitemap.xml in both positions of the switch', async () => {
  const offRobots = await get(closedURL, '/robots.txt');
  expect(offRobots.status).toBe(200);
  expect(offRobots.headers['content-type']).toBe('text/plain; charset=utf-8');
  expect(offRobots.html).toBe('User-agent: *\nDisallow: /\n');
  const onRobots = await get(indexedURL, '/robots.txt');
  expect(onRobots.html).toBe(
    `User-agent: *\nAllow: /\nDisallow: /api/\nDisallow: /download/get/\n\nSitemap: ${indexedURL}/sitemap.xml\n`,
  );
  const locs = (xml: string) => [...xml.matchAll(/<loc>([^<]*)<\/loc>/g)].map((m) => m[1]);
  const offMap = await get(closedURL, '/sitemap.xml');
  expect(offMap.status).toBe(200);
  expect(offMap.headers['content-type']).toBe('application/xml; charset=utf-8');
  expect(offMap.html).toContain('<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">');
  expect(locs(offMap.html)).toEqual([]);
  const onMap = await get(indexedURL, '/sitemap.xml');
  expect(locs(onMap.html)).toEqual(
    ['/', '/plans', '/download', '/guide'].map((p) => `${indexedURL}${p}`),
  );
  for (const answer of [offRobots, onRobots, offMap, onMap])
    expect(answer.headers['x-robots-tag']).toBe('noindex');
});

test('S1-05: the canonical address drops the query', async () => {
  for (const [path, canonical] of [
    ['/plans?cycle=yearly', '/plans'],
    ['/plans?plan=basic&pay=alipay', '/plans'],
    ['/register?ref=ABC123', '/register'],
    ['/login?next=%2Fdashboard', '/login'],
    ['/guide?platform=macos', '/guide'],
  ]) {
    const answer = await get(indexedURL, path);
    expect(canonicalOf(answer.head), path).toEqual([indexedURL + canonical]);
  }
});

test('S1-06: private pages stay noindex with the switch on', async () => {
  for (const [path, robots] of closedPages) {
    const answer = await get(indexedURL, path);
    expect(answer.status, path).toBe(200);
    expect(robotsOf(answer.head), path).toEqual([robots]);
    expect(canonicalOf(answer.html), path).toEqual([]);
    seen.push(answer.html);
  }
});

test('S1-07: the API proxy, the runtime configuration and the file redirects are marked', async () => {
  for (const base of [closedURL, indexedURL]) {
    for (const path of ['/api/runtime-config', '/api/v1/meta', '/api/v1/no-such-route'])
      expect((await get(base, path)).headers['x-robots-tag'], path).toBe('noindex, nofollow');
    const redirect = await get(base, '/download/get/android/arm64-v8a/apk');
    expect(redirect.status).toBe(302);
    expect(redirect.headers['x-robots-tag']).toBe('noindex');
  }
  // Crawlers are told not to follow the download buttons (D10).
  const page = await get(indexedURL, '/download');
  const buttons = [...page.html.matchAll(/<a\b[^>]*href="\/download\/get\/[^"]*"[^>]*>/g)].map(
    (m) => m[0],
  );
  expect(buttons.length).toBeGreaterThan(0);
  for (const button of buttons) expect(button).toContain('rel="nofollow"');
});

test('S1-08: a request for another host is never indexed and points at the canonical address', async () => {
  for (const host of ['old.example.test', `localhost:${new URL(indexedURL).port}`]) {
    for (const path of ['/', '/plans']) {
      const answer = await get(indexedURL, path, host);
      expect(answer.status, `${host}${path}`).toBe(200);
      expect(robotsOf(answer.head), `${host}${path}`).toEqual(['noindex, nofollow']);
      expect(canonicalOf(answer.head).map(same), `${host}${path}`).toEqual([
        same(indexedURL + path),
      ]);
    }
  }
  // The proper host still is.
  expect(robotsOf((await get(indexedURL, '/', new URL(indexedURL).host)).head)).toEqual([
    'index, follow',
  ]);
});

test('S1-09: a missing page answers 404 and noindex', async () => {
  for (const base of [closedURL, indexedURL]) {
    const answer = await get(base, '/no-such-page-anywhere');
    expect(answer.status).toBe(404);
    expect(robotsOf(answer.head).length).toBeGreaterThan(0);
    for (const value of robotsOf(answer.head)) expect(value).toContain('noindex');
    expect(canonicalOf(answer.html)).toEqual([]);
  }
});

test('S1-11: no served page names the API host or the internal API address', async () => {
  expect(seen.length).toBeGreaterThan(20);
  for (const html of seen) {
    expect(html).not.toContain('api.asterlink.vip');
    expect(html).not.toContain(fixture.api);
  }
});
