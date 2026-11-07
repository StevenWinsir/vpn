// Search engine rules (handoff_seo.md): round 1, SITE_URL and SITE_INDEXING, the policy table
// against the page files, robots.txt, sitemap.xml and the canonical host rule (S1-01, S1-02,
// S1-11); round 2, the page copy, the share image and the plans in the first HTML (S2-01, S2-03,
// S2-05, S2-06). The HTTP side runs in tests/seo.spec.ts.
import assert from 'node:assert/strict';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative, sep } from 'node:path';
import { test } from 'node:test';
import {
  canonicalHost,
  closedRobots,
  disallowedPaths,
  pageSEO,
  parseSiteURL,
  policies,
  robotsTxt,
  siteConfig,
  sitemapRoutes,
  sitemapXml,
} from '../src/lib/seo.ts';
import {
  downloadDescription,
  pageCopy,
  releasedPlatforms,
  shareText,
} from '../src/lib/seo-copy.ts';
import { shareImage } from '../src/lib/share-image.ts';
import {
  createCachedSource,
  fetchPublicJSON,
  publicStaleForMs,
  publicTTL,
  sanitizeMeta,
  sanitizePlans,
} from '../src/lib/public-source.ts';
import { shareModule } from './brand-icons.mjs';
import { bannedIn } from '../tests/copy-words.ts';

const frontend = new URL('..', import.meta.url).pathname;
const app = join(frontend, 'src', 'app');

test('SITE_URL: an HTTPS root origin, or plain HTTP on a loopback host (S1-01)', () => {
  const good = {
    'https://www.asterlink.vip': 'https://www.asterlink.vip',
    ' https://WWW.AsterLink.vip/ ': 'https://www.asterlink.vip',
    'https://www.asterlink.vip:443': 'https://www.asterlink.vip',
    'https://www.asterlink.vip:8443': 'https://www.asterlink.vip:8443',
    'http://localhost:3000': 'http://localhost:3000',
    'http://127.0.0.1:41234/': 'http://127.0.0.1:41234',
    'http://[::1]:3000': 'http://[::1]:3000',
  };
  for (const [raw, origin] of Object.entries(good)) assert.equal(parseSiteURL(raw), origin, raw);
  for (const raw of [
    undefined,
    '',
    '   ',
    'www.asterlink.vip',
    'http://www.asterlink.vip',
    'http://10.0.0.5:3000',
    'https://www.asterlink.vip/app',
    'https://www.asterlink.vip//',
    'https://www.asterlink.vip/?a=1',
    'https://www.asterlink.vip?',
    'https://www.asterlink.vip/#top',
    'https://user:secret@www.asterlink.vip',
    'https://user@www.asterlink.vip',
    'ftp://www.asterlink.vip',
    'https://www.aster link.vip',
    'javascript:alert(1)',
  ])
    assert.equal(parseSiteURL(raw), null, String(raw));
});

test('indexing needs SITE_INDEXING=on and a valid SITE_URL; each problem is named without its value (S1-01)', () => {
  const on = siteConfig({ SITE_URL: 'https://www.asterlink.vip', SITE_INDEXING: 'on' });
  assert.equal(on.indexing, true);
  assert.deepEqual(on.problems, []);
  assert.equal(
    siteConfig({ SITE_URL: 'https://www.asterlink.vip', SITE_INDEXING: ' ON ' }).indexing,
    true,
  );
  for (const env of [
    {},
    { SITE_URL: 'https://www.asterlink.vip' },
    { SITE_URL: 'https://www.asterlink.vip', SITE_INDEXING: 'off' },
    { SITE_URL: 'https://www.asterlink.vip', SITE_INDEXING: 'yes' },
    { SITE_URL: 'https://www.asterlink.vip', SITE_INDEXING: 'true' },
    { SITE_INDEXING: 'on' },
    { SITE_URL: 'http://www.asterlink.vip', SITE_INDEXING: 'on' },
  ])
    assert.equal(siteConfig(env).indexing, false, JSON.stringify(env));
  assert.deepEqual(siteConfig({}).problems, []);
  assert.deepEqual(
    siteConfig({ SITE_URL: 'https://www.asterlink.vip', SITE_INDEXING: 'off' }).problems,
    [],
  );
  const bad = siteConfig({
    SITE_URL: 'https://secret-host.example/path',
    SITE_INDEXING: 'on',
    SITE_VERIFICATION_GOOGLE: 'has space secret',
  });
  assert.equal(bad.problems.length, 3);
  for (const problem of bad.problems) {
    assert.doesNotMatch(problem, /secret/);
  }
});

test('verification values: letters, digits, "_" and "-" only, at most 100', () => {
  const config = siteConfig({
    SITE_VERIFICATION_GOOGLE: 'AbC_12-x',
    SITE_VERIFICATION_BING: '0123456789ABCDEF',
    SITE_VERIFICATION_BAIDU: 'codeva-<script>',
  });
  assert.deepEqual(config.verification, {
    google: 'AbC_12-x',
    bing: '0123456789ABCDEF',
    baidu: null,
  });
  assert.equal(config.problems.length, 1);
  assert.equal(siteConfig({ SITE_VERIFICATION_GOOGLE: 'a'.repeat(101) }).verification.google, null);
  assert.equal(
    siteConfig({ SITE_VERIFICATION_GOOGLE: 'a'.repeat(100) }).verification.google,
    'a'.repeat(100),
  );
});

test('the canonical host rule only accepts the configured host (D12)', () => {
  const origin = 'https://www.asterlink.vip';
  assert.equal(canonicalHost(origin, 'www.asterlink.vip'), true);
  assert.equal(canonicalHost(origin, 'WWW.ASTERLINK.VIP'), true);
  assert.equal(canonicalHost(origin, 'www.asterlink.vip:443'), true);
  for (const host of [
    null,
    undefined,
    '',
    'asterlink.vip',
    'api.asterlink.vip',
    'www.asterlink.vip:8443',
    '203.0.113.7',
    'www.asterlink.vip.evil.example',
  ])
    assert.equal(canonicalHost(origin, host), false, String(host));
  assert.equal(canonicalHost('http://127.0.0.1:4100', '127.0.0.1:4100'), true);
  assert.equal(canonicalHost('http://127.0.0.1:4100', '127.0.0.1'), false);
  assert.equal(canonicalHost('http://localhost:80', 'localhost'), true);
});

const indexed = siteConfig({ SITE_URL: 'https://www.asterlink.vip', SITE_INDEXING: 'on' });
const closed = siteConfig({ SITE_URL: 'https://www.asterlink.vip' });

test('pages: indexed with an absolute canonical only when the site is and the host matches', () => {
  assert.deepEqual(pageSEO('/plans', indexed, 'www.asterlink.vip'), {
    robots: { index: true, follow: true },
    canonical: 'https://www.asterlink.vip/plans',
  });
  assert.equal(pageSEO('/', indexed, 'www.asterlink.vip').canonical, 'https://www.asterlink.vip/');
  // Another host: never indexed, still pointing at the canonical address (D12).
  assert.deepEqual(pageSEO('/plans', indexed, '203.0.113.7'), {
    robots: { index: false, follow: false },
    canonical: 'https://www.asterlink.vip/plans',
  });
  // Indexing off: noindex and no canonical at all.
  assert.deepEqual(pageSEO('/plans', closed, 'www.asterlink.vip'), {
    robots: { index: false, follow: false },
    canonical: null,
  });
  assert.deepEqual(closedRobots('/dashboard'), { index: false, follow: true });
  assert.deepEqual(closedRobots('/admin/console'), { index: false, follow: false });
  assert.throws(() => closedRobots('/plans'));
});

test('robots.txt and sitemap.xml follow the switch (handoff_seo.md 4, D13)', () => {
  assert.equal(robotsTxt(closed), 'User-agent: *\nDisallow: /\n');
  assert.equal(
    robotsTxt(indexed),
    'User-agent: *\nAllow: /\nDisallow: /api/\nDisallow: /download/get/\n\nSitemap: https://www.asterlink.vip/sitemap.xml\n',
  );
  assert.deepEqual(disallowedPaths, ['/api/', '/download/get/']);
  // The private pages are left to their noindex: robots.txt must not hide them (D10).
  assert.doesNotMatch(robotsTxt(indexed), /admin|dashboard|account/);
  assert.deepEqual(sitemapRoutes, ['/', '/plans', '/download', '/guide']);
  const locs = (xml) => [...xml.matchAll(/<loc>([^<]*)<\/loc>/g)].map((m) => m[1]);
  assert.deepEqual(locs(sitemapXml(indexed)), [
    'https://www.asterlink.vip/',
    'https://www.asterlink.vip/plans',
    'https://www.asterlink.vip/download',
    'https://www.asterlink.vip/guide',
  ]);
  assert.deepEqual(locs(sitemapXml(closed)), []);
  assert.match(
    sitemapXml(closed),
    /^<\?xml version="1\.0" encoding="UTF-8"\?>\n<urlset xmlns="http:\/\/www\.sitemaps\.org\/schemas\/sitemap\/0\.9">\n<\/urlset>\n$/,
  );
  for (const route of sitemapRoutes) assert.equal(policies[route].index, true, route);
});

function pageFiles(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return pageFiles(path);
    return name === 'page.tsx' ? [path] : [];
  });
}

const routeOf = (file) => {
  const dir = relative(app, file).split(sep).slice(0, -1).join('/');
  return dir === '' ? '/' : `/${dir}`;
};

test('every page of src/app is in the policy table and declares its own value (S1-02)', () => {
  const files = pageFiles(app);
  assert.ok(files.length >= 25, `only ${files.length} pages found`);
  const seen = new Set();
  const consoleLayout = readFileSync(join(app, 'admin', 'console', 'layout.tsx'), 'utf8');
  assert.match(consoleLayout, /closedRobots\('\/admin\/console'\)/);
  for (const file of files) {
    const route = routeOf(file);
    const source = readFileSync(file, 'utf8');
    if (route === '/admin/console' || route.startsWith('/admin/console/')) {
      // The console layout speaks for these; a page must not try to open itself.
      assert.doesNotMatch(source, /indexableMetadata|robots/, route);
      seen.add('/admin/console');
      continue;
    }
    const policy = policies[route];
    assert.ok(policy, `${route} is missing from the policy table in src/lib/seo.ts`);
    seen.add(route);
    if (policy.index)
      assert.ok(
        source.includes(`indexableMetadata('${route}'`),
        `${route} must use indexableMetadata('${route}')`,
      );
    else
      assert.ok(
        source.includes(`closedRobots('${route}')`),
        `${route} must set robots: closedRobots('${route}')`,
      );
  }
  assert.deepEqual([...seen].sort(), Object.keys(policies).sort(), 'every table entry has a page');
});

test('no page file sets a robots value of its own outside the table', () => {
  for (const file of pageFiles(app)) {
    const source = readFileSync(file, 'utf8');
    assert.doesNotMatch(source, /robots:\s*\{/, routeOf(file));
  }
});

// S1-11: the internal API host never ends up in what the website serves. Needs a build; CI runs
// next build before this script.
test(
  'the build output never names the API host',
  { skip: !existsSync(join(frontend, '.next', 'BUILD_ID')) && 'no build' },
  () => {
    const hits = [];
    const walk = (dir) => {
      for (const name of readdirSync(dir)) {
        const path = join(dir, name);
        if (name === 'cache') continue;
        if (statSync(path).isDirectory()) walk(path);
        else if (
          /\.(js|html|rsc|json|txt|css|map|body|meta)$/.test(name) &&
          readFileSync(path, 'utf8').includes('api.asterlink.vip')
        )
          hits.push(relative(frontend, path));
      }
    };
    walk(join(frontend, '.next'));
    assert.deepEqual(hits, []);
  },
);

// ---- Round 2 (handoff_seo.md 5): copy, share image, the plans in the first HTML ----

test('every indexable page has its own title and description within D7 (S2-01)', () => {
  const routes = Object.keys(policies).filter((r) => policies[r].index);
  assert.deepEqual(
    Object.keys(pageCopy).sort(),
    routes.sort(),
    'copy for exactly the indexable pages',
  );
  const len = (s) => [...s].length;
  const entries = Object.entries(pageCopy).map(([route, c]) => [
    route,
    route === '/download' ? { ...c, description: downloadDescription(released([])) } : c,
  ]);
  for (const [route, { title, description }] of entries) {
    assert.ok(len(title) <= 30, `${route} title has ${len(title)} characters`);
    if (route === '/') assert.match(title, /^AsterLink · /);
    else assert.match(title, / · AsterLink$/, route);
    assert.ok(
      len(description) >= 60 && len(description) <= 120,
      `${route} description has ${len(description)}`,
    );
    for (const text of [title, description]) {
      assert.deepEqual(bannedIn(text), [], `${route}: ${text}`);
      assert.doesNotMatch(text, /vpn|翻墙|科学上网/i, route);
    }
  }
  const titles = entries.map(([, c]) => c.title);
  const descriptions = entries.map(([, c]) => c.description);
  assert.equal(new Set(titles).size, titles.length, 'titles are unique');
  assert.equal(new Set(descriptions).size, descriptions.length, 'descriptions are unique');
  assert.deepEqual(bannedIn(Object.values(shareText).join(' ')), []);
});

const entry = (platform) => ({ platform, arch: 'x', format: 'y', version: '1.0.0' });
const released = (platforms) => ({ status: 'ok', entries: platforms.map(entry) });

test('the download description names exactly the released clients (S2-06)', () => {
  const len = (s) => [...s].length;
  const all = ['windows', 'macos', 'android', 'ios', 'linux'];
  const generic = downloadDescription(released([]));
  assert.equal(downloadDescription({ status: 'unavailable', entries: [] }), generic);
  assert.doesNotMatch(generic, /Windows|macOS|Android|iPhone|Linux/);
  const names = {
    windows: 'Windows',
    macos: 'macOS',
    android: 'Android',
    ios: 'iPhone',
    linux: 'Linux',
  };
  for (let mask = 1; mask < 1 << all.length; mask++) {
    const on = all.filter((_, i) => mask & (1 << i));
    const text = downloadDescription(released([...on].reverse()));
    assert.deepEqual(releasedPlatforms(released(on)), on);
    for (const p of all) assert.equal(text.includes(names[p]), on.includes(p), `${on}: ${p}`);
    assert.ok(text.includes(on.map((p) => names[p]).join('、')), 'in the page order');
    assert.ok(len(text) >= 60 && len(text) <= 120, `${on}: ${len(text)}`);
    assert.deepEqual(bannedIn(text), []);
  }
});

test('the share image is a committed 1200 x 630 PNG under 150 KB, versioned by its content (S2-05)', () => {
  const png = readFileSync(join(frontend, 'public', 'share', 'asterlink-share.png'));
  assert.equal(png.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');
  assert.equal(png.subarray(12, 16).toString(), 'IHDR');
  assert.deepEqual([png.readUInt32BE(16), png.readUInt32BE(20)], [1200, 630]);
  assert.ok(png.length < 150 * 1024, `${png.length} bytes`);
  assert.equal(
    readFileSync(join(frontend, 'src', 'lib', 'share-image.ts'), 'utf8'),
    shareModule(png),
    'src/lib/share-image.ts is current: run npm run brand:icons',
  );
  assert.deepEqual(shareImage, {
    path: '/share/asterlink-share.png',
    version: shareImage.version,
    width: 1200,
    height: 630,
  });
});

const tier = (price, extra = {}) => ({
  price_cents: price,
  original_cents: price,
  saving_cents: 0,
  discount_percent: 100,
  duration_days: 30,
  traffic_bytes: 50 * 1024 ** 3,
  monthly_equivalent_cents: price,
  ...extra,
});
const rawPlan = (id, extra = {}) => ({
  id,
  name: `计划 ${id}`,
  description: '说明',
  price_cents: 1500,
  duration_days: 30,
  traffic_bytes: 50 * 1024 ** 3,
  max_devices: 3,
  allow_dedicated: false,
  featured: false,
  monthly: tier(1500),
  yearly: tier(16200, {
    duration_days: 365,
    quota_cycle_days: 30,
    quota_cycle_bytes: 50 * 1024 ** 3,
  }),
  ...extra,
});

test('the plans for the first HTML are rebuilt field by field (D8, 5.3)', () => {
  const answer = sanitizePlans({
    plans: [
      rawPlan('a', { secret_cost: 1, name: '名称 </script><b>"x"</b>' }),
      rawPlan('b', { yearly: null }),
      rawPlan('a'),
      rawPlan('bad id!'),
      rawPlan('c', { price_cents: -1 }),
      rawPlan('d', { monthly: { price_cents: 1 } }),
      'nonsense',
    ],
    yearly_discount_percent: 90,
    max_yearly_saving_percent: 10,
    merchant: 'x',
  });
  assert.deepEqual(
    answer.plans.map((p) => p.id),
    ['a', 'b'],
  );
  assert.equal(
    answer.plans[0].name,
    '名称 </script><b>"x"</b>',
    'text is kept as is; React escapes it',
  );
  assert.equal('secret_cost' in answer.plans[0], false);
  assert.equal('merchant' in answer, false);
  assert.equal(answer.plans[1].yearly, null, 'no yearly tier stays null');
  assert.equal(answer.plans[0].yearly.quota_cycle_days, 30);
  // A backend from before the yearly tier: the fields stay absent, not null.
  const before = rawPlan('o');
  delete before.monthly;
  delete before.yearly;
  delete before.featured;
  const old = sanitizePlans({ plans: [before] });
  assert.equal(old.plans.length, 1);
  assert.equal('yearly' in old.plans[0] || 'monthly' in old.plans[0], false);
  assert.equal('max_yearly_saving_percent' in old, false);
  for (const raw of [null, 'x', {}, { plans: 'x' }]) assert.equal(sanitizePlans(raw), null);
  assert.deepEqual(sanitizePlans({ plans: [] }).plans, []);
});

test('the public capabilities are rebuilt from the known fields only', () => {
  const meta = sanitizeMeta({
    test_purchase_enabled: false,
    proxy_service_ready: true,
    currency: 'CNY',
    traffic_unit: 'GB',
    payment_enabled: true,
    payment_methods: ['alipay', 'wxpay'],
    plan_upgrade_supported: 'yes',
    merchant_key: 'secret',
  });
  assert.deepEqual(meta, {
    test_purchase_enabled: false,
    proxy_service_ready: true,
    currency: 'CNY',
    traffic_unit: 'GB',
    payment_enabled: true,
    payment_methods: ['alipay', 'wxpay'],
  });
  assert.equal(sanitizeMeta({ proxy_service_ready: true }), null);
});

test('the plan cache: a minute fresh, the last good copy for a day, a short pause after a failure (S2-03)', async () => {
  let clock = 0;
  let calls = 0;
  let answer = () => ({ plans: [rawPlan('a')] });
  const source = createCachedSource({
    fetchJSON: async () => {
      calls++;
      return answer();
    },
    parse: sanitizePlans,
    now: () => clock,
  });
  const [x, y] = await Promise.all([source.get(), source.get()]);
  assert.equal(calls, 1, 'one request for concurrent pages');
  assert.equal(x, y);
  clock += publicTTL - 1;
  await source.get();
  assert.equal(calls, 1);
  clock += 1;
  answer = () => {
    throw new Error('503');
  };
  assert.deepEqual(
    (await source.get()).plans.map((p) => p.id),
    ['a'],
    'the last good copy',
  );
  assert.equal(calls, 2);
  await source.get();
  assert.equal(calls, 2, 'not asked again within five seconds');
  clock += 5000;
  answer = () => ({ unexpected: true });
  assert.deepEqual(
    (await source.get()).plans.map((p) => p.id),
    ['a'],
    'an unexpected answer is a failure',
  );
  assert.equal(calls, 3);
  clock += publicStaleForMs;
  assert.equal(await source.get(), null, 'nothing older than a day');
  const empty = createCachedSource({
    fetchJSON: async () => {
      throw new Error('down');
    },
    parse: sanitizePlans,
    now: () => 0,
  });
  assert.equal(await empty.get(), null, 'never read: the page loads the plans itself');
});

test('a request for the public answers has a timeout, no redirects and no cache', async () => {
  const seen = [];
  const fake = async (url, init) => {
    seen.push({ url, init });
    return new Response('{"plans":[]}', { status: 200 });
  };
  assert.deepEqual(
    await fetchPublicJSON(fake, new URL('http://127.0.0.1:8080/api/v1/'), '/plans'),
    { plans: [] },
  );
  assert.equal(seen[0].url, 'http://127.0.0.1:8080/api/v1/plans');
  assert.equal(seen[0].init.redirect, 'error');
  assert.equal(seen[0].init.cache, 'no-store');
  assert.ok(seen[0].init.signal instanceof AbortSignal);
  await assert.rejects(
    fetchPublicJSON(
      async () => new Response('x', { status: 503 }),
      new URL('http://h/api/v1'),
      '/plans',
    ),
  );
});
