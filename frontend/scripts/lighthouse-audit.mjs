#!/usr/bin/env node
// Lighthouse mobile audit of the public pages against the budget of handoff_seo.md D18.
//
//   npm run build
//   npm run audit:lighthouse -- [--runs 3] [--out DIR] [--report-only | --gate stable]
//
// The production build in .next is started on a loopback port with search engine indexing on
// (the SEO category judges an indexable page) against a small built-in backend that answers the
// public /plans, /meta and /downloads with fixed data, so the pages are measured with prices and
// download cards, as visitors see them. Nothing else is contacted. Lighthouse runs through npx at
// a pinned version, so it is never a dependency of the website; Chrome comes from CHROME_PATH,
// PLAYWRIGHT_CHROME_PATH or the usual macOS location. Each page runs three times and every figure
// is the median (D18). The exit status is 1 when a page is over the budget, unless --report-only.
// CI runs it with --gate stable: every figure is recorded, but only SEO, accessibility and CLS can
// fail the job, because timings on a shared machine move with its load (handoff_seo.md 7.1).
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { join, resolve } from 'node:path';
import {
  auditPages,
  failingAudits,
  figures,
  medians,
  overBudget,
  tableHead,
  tableRow,
} from './lighthouse-budget.mjs';

const LIGHTHOUSE = 'lighthouse@13.6.0';
const frontend = new URL('..', import.meta.url).pathname;

function option(name, fallback) {
  const at = process.argv.indexOf(`--${name}`);
  return at > 0 ? process.argv[at + 1] : fallback;
}
const runs = Number(option('runs', '3'));
const reportOnly = process.argv.includes('--report-only');
const gate = option('gate', 'all');
if (!['all', 'stable'].includes(gate)) throw new Error('--gate takes all or stable');
const out = resolve(
  option(
    'out',
    join(
      frontend,
      '..',
      'artifacts',
      `lighthouse-${new Date().toISOString().replace(/[:.]/g, '-')}`,
    ),
  ),
);
if (!Number.isInteger(runs) || runs < 1 || runs > 9) throw new Error('--runs takes 1 to 9');
if (!existsSync(join(frontend, '.next', 'BUILD_ID'))) throw new Error('run npm run build first');
if (existsSync(join(out, 'summary.json')))
  throw new Error(`${out} holds a summary; use a new --out`);
mkdirSync(out, { recursive: true });

const chrome = [
  process.env.CHROME_PATH,
  process.env.PLAYWRIGHT_CHROME_PATH,
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
].find((path) => path && existsSync(path));
if (!chrome) throw new Error('Chrome not found: set CHROME_PATH');

// ---- the fixed public backend -----------------------------------------------------------------

const GB = 1024 ** 3;
const tier = (price, days, gb, equivalent, original = price, cycle) => ({
  price_cents: price,
  original_cents: original,
  saving_cents: original - price,
  discount_percent: Math.round((price / original) * 100),
  duration_days: days,
  traffic_bytes: gb * GB,
  monthly_equivalent_cents: equivalent,
  ...(cycle ? { quota_cycle_days: 30, quota_cycle_bytes: cycle * GB } : {}),
});
const plan = (id, name, description, price, gb, devices, dedicated, featured) => ({
  id,
  name,
  description,
  price_cents: price,
  duration_days: 30,
  traffic_bytes: gb * GB,
  max_devices: devices,
  allow_dedicated: dedicated,
  featured,
  monthly: tier(price, 30, gb, price),
  yearly: tier(
    Math.round(price * 12 * 0.85),
    360,
    gb * 12,
    Math.round(price * 0.85),
    price * 12,
    gb,
  ),
});
const answers = {
  '/plans': {
    plans: [
      plan('starter', '轻量计划', '日常浏览与社交', 1900, 100, 2, false, false),
      plan('plus', '进阶计划', '高清视频与多设备', 3900, 300, 4, true, true),
      plan('max', '专业计划', '大流量与专线优先', 6900, 800, 6, true, false),
    ],
    yearly_discount_percent: 85,
    max_yearly_saving_percent: 15,
  },
  '/meta': {
    test_purchase_enabled: false,
    proxy_service_ready: true,
    currency: 'CNY',
    traffic_unit: 'GB',
    payment_enabled: true,
    payment_methods: ['alipay', 'wxpay'],
    allow_registration: true,
    password_reset_enabled: true,
    referral_enabled: true,
    tickets_enabled: true,
    plan_upgrade_supported: true,
  },
  '/downloads': {
    downloads: [
      {
        platform: 'windows',
        arch: 'x64',
        format: 'exe',
        app_version: '1.2.3',
        size_bytes: 48_000_000,
        sha256: 'a'.repeat(64),
      },
      {
        platform: 'macos',
        arch: 'arm64',
        format: 'dmg',
        app_version: '1.2.3',
        size_bytes: 52_000_000,
        sha256: 'b'.repeat(64),
      },
      {
        platform: 'android',
        arch: 'arm64-v8a',
        format: 'apk',
        app_version: '1.2.3',
        size_bytes: 38_000_000,
        sha256: 'c'.repeat(64),
      },
    ],
  },
};

function listen(server) {
  return new Promise((done) => server.listen(0, '127.0.0.1', () => done(server.address().port)));
}

const api = createServer((req, res) => {
  const path = new URL(req.url, 'http://x').pathname.replace(/^\/api\/v1/, '');
  const body = answers[path];
  // Signed out everywhere else: the pages are measured as a first-time visitor sees them.
  const status = body ? 200 : path.startsWith('/auth/') ? 401 : 404;
  res.writeHead(status, { 'content-type': 'application/json' });
  res.end(JSON.stringify(body ?? { error: status === 401 ? 'unauthorized' : 'not_found' }));
});
const apiPort = await listen(api);

// ---- the website ------------------------------------------------------------------------------

const probe = createServer();
const sitePort = await listen(probe);
await new Promise((done) => probe.close(done));
const origin = `http://127.0.0.1:${sitePort}`;
const env = Object.fromEntries(
  Object.entries(process.env).filter(
    ([key]) => !/^(API_|SITE_|NEXT_PUBLIC_|CLIENT_IP_HEADER|CRISP_)/.test(key),
  ),
);
const site = spawn(
  process.execPath,
  ['node_modules/next/dist/bin/next', 'start', '-H', '127.0.0.1', '-p', String(sitePort)],
  {
    cwd: frontend,
    env: {
      ...env,
      NODE_ENV: 'production',
      API_INTERNAL_URL: `http://127.0.0.1:${apiPort}/api/v1`,
      SITE_URL: origin,
      SITE_INDEXING: 'on',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  },
);
let siteLog = '';
site.stdout.on('data', (chunk) => (siteLog += chunk));
site.stderr.on('data', (chunk) => (siteLog += chunk));

async function ready() {
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(`${origin}/robots.txt`)).ok) return;
    } catch {
      // not listening yet
    }
    await new Promise((done) => setTimeout(done, 200));
  }
  throw new Error(`the website did not start:\n${siteLog}`);
}

function lighthouse(url, file) {
  return new Promise((done, fail) => {
    const child = spawn(
      'npx',
      [
        '-y',
        LIGHTHOUSE,
        url,
        '--quiet',
        '--output=json',
        `--output-path=${file}`,
        '--only-categories=performance,accessibility,seo',
        // CI runners (Ubuntu 24.04) refuse Chrome's user-namespace sandbox to unprivileged users.
        `--chrome-flags=--headless=new${process.env.CI ? ' --no-sandbox' : ''}`,
      ],
      {
        cwd: out,
        env: { ...process.env, CHROME_PATH: chrome },
        stdio: ['ignore', 'inherit', 'inherit'],
      },
    );
    child.on('exit', (code) =>
      code === 0 ? done() : fail(new Error(`lighthouse exited ${code}`)),
    );
  });
}

const summary = { lighthouse: LIGHTHOUSE, runs, gate, origin, pages: {} };
const lines = [...tableHead];
let over = false;
try {
  await ready();
  for (const page of auditPages) {
    // One request first, so the server-side caches and the compiled route are warm (a visitor
    // never meets a cold process twice).
    await fetch(`${origin}${page}`).then((r) => r.text());
    const reports = [];
    for (let run = 1; run <= runs; run++) {
      const file = join(
        out,
        `${page === '/' ? 'home' : page.slice(1).replace(/\//g, '-')}-${run}.json`,
      );
      await lighthouse(`${origin}${page}`, file);
      reports.push(JSON.parse(readFileSync(file, 'utf8')));
    }
    const m = medians(reports.map(figures));
    const problems = overBudget(m);
    over ||= overBudget(m, { stableOnly: gate === 'stable' }).length > 0;
    summary.pages[page] = {
      median: m,
      runs: reports.map(figures),
      overBudget: problems,
      failingAudits: failingAudits(reports),
    };
    lines.push(tableRow(page, m, problems));
    console.log(lines.at(-1));
  }
} finally {
  site.kill();
  api.close();
}
writeFileSync(join(out, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`);
writeFileSync(join(out, 'summary.md'), `${lines.join('\n')}\n`);
console.log(`\n${lines.join('\n')}\n\nreports: ${out}`);
for (const [page, result] of Object.entries(summary.pages))
  for (const audit of result.failingAudits)
    console.log(`${page}: ${audit.category} audit "${audit.id}" (${audit.title}) did not pass`);
if (over && !reportOnly) process.exitCode = 1;
