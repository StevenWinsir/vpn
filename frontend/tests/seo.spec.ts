import { expect, test } from '@playwright/test';
import { request as httpRequest } from 'node:http';
import { readFile } from 'node:fs/promises';
import { bannedIn } from './copy-words';
import type { DownloadState } from '../src/lib/downloads';
import { previewPlans } from '../src/lib/home';
import { downloadDescription, pageCopy } from '../src/lib/seo-copy';
import { shareImage } from '../src/lib/share-image';
import { tierOf, trafficLabel } from '../src/lib/shop';
import { money, type Plan, type PlansResponse } from '../src/lib/types';

// Search engine acceptance (handoff_seo.md 9), round 1 (S1-03 … S1-09, S1-11) and round 2 (S2-01 …
// S2-08): plain HTTP, no browser page, except S2-04, which watches the first paint. scripts/test-payment-web.py runs one build twice: PLAYWRIGHT_BASE_URL without the
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

// ---- Round 2 (handoff_seo.md 5, S2-01 … S2-07): copy, share card, plans in the first HTML ----

const metaOf = (part: string, key: string) =>
  [
    ...part.matchAll(
      new RegExp(
        `<meta (?:name|property)="${key.replace(/[.:]/g, '\\$&')}" content="([^"]*)"\\s*/?>`,
        'g',
      ),
    ),
  ].map((m) => decode(m[1]));
const titleOf = (head: string) => decode(/<title>([^<]*)<\/title>/.exec(head)?.[1] ?? '');
function decode(text: string): string {
  return text
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, '&');
}
// What a visitor without JavaScript reads: no scripts (the page data rides in them too), no tags.
function textOf(html: string): string {
  return decode(
    html
      .replace(/<script\b[\s\S]*?<\/script>/g, ' ')
      .replace(/<style\b[\s\S]*?<\/style>/g, ' ')
      .replace(/<!-- -->/g, '')
      .replace(/<[^>]+>/g, ' '),
  ).replace(/\s+/g, ' ');
}
const markupOf = (html: string) =>
  html.replace(/<script\b[\s\S]*?<\/script>/g, '').replace(/<template\b[\s\S]*?<\/template>/g, '');

async function setDownloads(set: 'empty' | 'base') {
  const response = await fetch(`${fixture.api}/fixture/downloads?set=${set}`, {
    method: 'POST',
    headers: { 'X-Acceptance-Control': fixture.control },
  });
  expect(response.status).toBe(204);
  await new Promise((resolve) => setTimeout(resolve, 1200));
}

test('S2-01: each indexable page has the title and description of src/lib/seo-copy.ts', async () => {
  const titles = new Set<string>();
  const descriptions = new Set<string>();
  for (const path of indexable) {
    const page = await get(indexedURL, path);
    const copy = pageCopy[path as keyof typeof pageCopy];
    expect(titleOf(page.head), path).toBe(copy.title);
    const [description] = metaOf(page.head, 'description');
    if (path === '/download') expect(description).toMatch(/^下载 AsterLink 官方/);
    else expect(description, path).toBe(copy.description);
    expect(bannedIn(titleOf(page.head) + description), path).toEqual([]);
    expect(titleOf(page.head) + description, path).not.toMatch(/vpn|翻墙|科学上网/i);
    titles.add(titleOf(page.head));
    descriptions.add(description);
    // The switch does not change the words.
    expect(titleOf((await get(closedURL, path)).head), path).toBe(copy.title);
  }
  expect(titles.size).toBe(indexable.length);
  expect(descriptions.size).toBe(indexable.length);
});

test('S2-02: without JavaScript the home page and /plans carry every plan on sale', async () => {
  const response = (await (await fetch(`${closedURL}/api/v1/plans`)).json()) as PlansResponse;
  expect(response.plans.length).toBeGreaterThan(0);
  const expectations = (plans: Plan[]) =>
    plans.map((plan) => {
      const tier = tierOf(plan, 'monthly')!;
      return [
        plan.name,
        money(tier.price_cents),
        `${trafficLabel(tier)} 计费流量`,
        `最多 ${plan.max_devices} 台设备同时在线`,
      ];
    });
  for (const base of [closedURL, indexedURL]) {
    const plans = await get(base, '/plans');
    const text = textOf(plans.body);
    for (const facts of expectations(response.plans))
      for (const fact of facts) expect(text, `/plans ${fact}`).toContain(fact);
    expect(markupOf(plans.body)).not.toContain('mantine-Skeleton');
    expect(markupOf(plans.body).match(/data-testid="plan-card"/g)?.length).toBe(
      response.plans.length,
    );
    const home = await get(base, '/');
    const homeText = textOf(home.body);
    const preview = previewPlans(response.plans);
    for (const facts of expectations(preview))
      for (const fact of facts) expect(homeText, `/ ${fact}`).toContain(fact);
    expect(markupOf(home.body)).not.toContain('plan-skeleton');
    expect(markupOf(home.body).match(/data-testid="home-plan"/g)?.length).toBe(preview.length);
  }
  // The yearly terms in the address are rendered on the server as well.
  const yearly = response.plans.filter((p) => p.yearly);
  if (yearly.length) {
    const text = textOf((await get(closedURL, '/plans?cycle=yearly')).body);
    for (const plan of yearly) expect(text).toContain(money(plan.yearly!.price_cents));
  }
});

test('S2-03: without the plans the pages still answer 200 and keep their browser-side loading', async () => {
  const offline = process.env.DOWNLOAD_OFFLINE_URL;
  if (!offline) throw new Error('DOWNLOAD_OFFLINE_URL required (scripts/test-payment-web.py)');
  const home = await get(offline, '/');
  expect(home.status).toBe(200);
  expect(markupOf(home.body)).not.toContain('data-testid="home-plan"');
  expect(textOf(home.body)).toContain('查看全部套餐');
  const plans = await get(offline, '/plans');
  expect(plans.status).toBe(200);
  expect(markupOf(plans.body)).not.toContain('data-testid="plan-card"');
  expect(markupOf(plans.body)).toContain('mantine-Skeleton');
  expect(textOf(plans.body)).toContain('找到适合你的连接计划');
});

test('S2-05: every indexable page has a share card with absolute addresses and the 1200 x 630 image', async () => {
  const imageURL = `${indexedURL}${shareImage.path}?v=${shareImage.version}`;
  for (const path of indexable) {
    const { head } = await get(indexedURL, path);
    const copy = pageCopy[path as keyof typeof pageCopy];
    expect(metaOf(head, 'og:title'), path).toEqual([copy.title]);
    expect(metaOf(head, 'og:description'), path).toEqual(metaOf(head, 'description'));
    expect(metaOf(head, 'og:url').map(same), path).toEqual([same(indexedURL + path)]);
    expect(metaOf(head, 'og:image'), path).toEqual([imageURL]);
    expect(metaOf(head, 'og:image:width'), path).toEqual(['1200']);
    expect(metaOf(head, 'og:image:height'), path).toEqual(['630']);
    expect(metaOf(head, 'og:locale'), path).toEqual(['zh_CN']);
    expect(metaOf(head, 'og:type'), path).toEqual(['website']);
    expect(metaOf(head, 'og:site_name'), path).toEqual(['AsterLink']);
    expect(metaOf(head, 'twitter:card'), path).toEqual(['summary_large_image']);
    expect(metaOf(head, 'twitter:image'), path).toEqual([imageURL]);
    // Without SITE_URL there is no address to give: text only, never a guessed host.
    const off = (await get(closedURL, path)).head;
    expect(metaOf(off, 'og:title'), path).toEqual([copy.title]);
    expect(metaOf(off, 'og:url'), path).toEqual([]);
    expect(metaOf(off, 'og:image'), path).toEqual([]);
    expect(metaOf(off, 'twitter:card'), path).toEqual(['summary']);
  }
  const image = await fetch(imageURL);
  expect(image.status).toBe(200);
  expect(image.headers.get('content-type')).toBe('image/png');
  const png = Buffer.from(await image.arrayBuffer());
  expect(png.subarray(1, 4).toString()).toBe('PNG');
  expect([png.readUInt32BE(16), png.readUInt32BE(20)]).toEqual([1200, 630]);
  // Private pages are never shared as a product page.
  expect(metaOf((await get(indexedURL, '/dashboard')).head, 'og:image')).toEqual([]);
});

test('S2-06: the download description follows the released clients', async () => {
  const cards = (body: string) =>
    [...markupOf(body).matchAll(/data-platform="([a-z]+)"/g)].map((m) => m[1]);
  // The fixture's base set: Android only.
  let page = await get(indexedURL, '/download');
  let [description] = metaOf(page.head, 'description');
  expect(description).toBe(
    downloadDescription({ status: 'ok', entries: [{ platform: 'android' }] } as DownloadState),
  );
  expect(description).toContain('Android');
  expect(description).not.toMatch(/Windows|macOS|iPhone|Linux/);
  expect(cards(page.body)).toContain('android');
  expect(markupOf(page.body)).toContain('/download/get/android/');
  await setDownloads('empty');
  try {
    page = await get(indexedURL, '/download');
    [description] = metaOf(page.head, 'description');
    expect(description).toBe(downloadDescription({ status: 'ok', entries: [] }));
    expect(description).not.toMatch(/Windows|macOS|Android|iPhone|Linux/);
    expect(markupOf(page.body)).not.toContain('/download/get/');
  } finally {
    await setDownloads('base');
  }
});

test('S2-07: one h1 per indexable page and no skipped heading level', async () => {
  for (const base of [closedURL, indexedURL])
    for (const path of indexable) {
      const levels = [...markupOf((await get(base, path)).body).matchAll(/<h([1-6])\b/g)].map((m) =>
        Number(m[1]),
      );
      expect(levels.filter((l) => l === 1).length, `${path} h1`).toBe(1);
      expect(levels[0], `${path} starts with its h1`).toBe(1);
      levels.forEach((level, i) => {
        if (i > 0)
          expect(level - levels[i - 1], `${path} ${levels.join(' ')}`).toBeLessThanOrEqual(1);
      });
    }
});

test('S2-08: the home page links to the public pages with plain links', async () => {
  const markup = markupOf((await get(closedURL, '/')).body);
  for (const href of ['/plans', '/download', '/guide', '/register'])
    expect(markup, href).toMatch(new RegExp(`<a [^>]*href="${href}"`));
  expect(markup).toMatch(/<footer[\s\S]*<a [^>]*href="\/guide"[^>]*>使用教程<\/a>/);
});

test('S2-04: the plan cards are there at the first paint, never a skeleton first', async ({
  page,
}) => {
  await page.addInitScript(() => {
    const record = { skeleton: false, card: -1 };
    (window as unknown as { __plans: typeof record }).__plans = record;
    new MutationObserver(() => {
      if (document.querySelector('[data-testid="plan-skeleton"], .mantine-Skeleton-root'))
        record.skeleton = true;
      if (
        record.card < 0 &&
        document.querySelector('[data-testid="home-plan"], [data-testid="plan-card"]')
      )
        record.card = performance.now();
    }).observe(document, { childList: true, subtree: true });
  });
  for (const [path, card] of [
    ['/', 'home-plan'],
    ['/plans', 'plan-card'],
  ]) {
    await page.goto(path);
    await expect(page.getByTestId(card).first()).toBeVisible();
    await page.waitForLoadState('networkidle');
    const result = await page.evaluate(() => {
      const paint = performance.getEntriesByName('first-contentful-paint')[0];
      return {
        ...(window as unknown as { __plans: { skeleton: boolean; card: number } }).__plans,
        paint: paint ? paint.startTime : null,
      };
    });
    expect(result.skeleton, path).toBe(false);
    expect(result.card, path).toBeGreaterThanOrEqual(0);
    expect(result.paint, path).not.toBeNull();
    expect(
      result.card,
      `${path}: cards at ${result.card} ms, first paint at ${result.paint} ms`,
    ).toBeLessThanOrEqual(result.paint!);
  }
});

test('S1-11: no served page names the API host or the internal API address', async () => {
  expect(seen.length).toBeGreaterThan(20);
  for (const html of seen) {
    expect(html).not.toContain('api.asterlink.vip');
    expect(html).not.toContain(fixture.api);
  }
});
