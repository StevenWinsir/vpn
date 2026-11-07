// Search engine rules (handoff_seo.md round 1): SITE_URL and SITE_INDEXING, the policy table
// against the page files, robots.txt, sitemap.xml and the canonical host rule (S1-01, S1-02,
// S1-11). The HTTP side runs in tests/seo.spec.ts.
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
