import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { once } from 'node:events';
import {
  access,
  copyFile,
  mkdtemp,
  readFile,
  readdir,
  rm,
  symlink,
  writeFile,
} from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const testUser = {
  id: 'proxy-test-user',
  name: '代理测试',
  email: 'proxy@example.invalid',
  role: 'user',
  created_at: '2025-01-01T00:00:00Z',
};

async function listen(server, port = 0) {
  server.listen(port, '127.0.0.1');
  await once(server, 'listening');
  return server.address().port;
}
async function close(server) {
  if (!server.listening) return;
  const closed = once(server, 'close');
  server.close();
  server.closeAllConnections();
  await closed;
}
async function freePort() {
  const server = createServer();
  const port = await listen(server);
  await close(server);
  return port;
}
async function staticFingerprint(directory) {
  const hash = createHash('sha256');
  async function walk(current) {
    for (const entry of (await readdir(current, { withFileTypes: true })).sort((a, b) =>
      a.name.localeCompare(b.name),
    )) {
      const filename = path.join(current, entry.name);
      if (entry.isDirectory()) await walk(filename);
      else {
        const bytes = await readFile(filename);
        hash.update(path.relative(directory, filename));
        hash.update(bytes);
        if (filename.endsWith('.js'))
          assert.doesNotMatch(
            bytes.toString(),
            /http:\/\/(?:localhost|127\.0\.0\.1):8080|API_INTERNAL_URL/,
          );
      }
    }
  }
  await walk(directory);
  return hash.digest('hex');
}

function mockBackend(label, origins) {
  const received = [];
  let generation = 0;
  let active = false;
  const server = createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const body = Buffer.concat(chunks).toString();
    const record = { method: request.method, url: request.url, headers: request.headers, body };
    received.push(record);
    const pathname = new URL(request.url, 'http://test.invalid').pathname;
    const cookie = request.headers.cookie || '';
    const origin = request.headers.origin;
    const json = (status, data) => {
      response.writeHead(status, {
        'Content-Type': 'application/json',
        'Cache-Control': 'no-store',
      });
      response.end(JSON.stringify(data));
    };
    const fail = (status, code, message = code) => json(status, { error: { code, message } });
    const cookies = (clear = false, secure = false) => {
      const attributes = `Path=/api/v1; HttpOnly; SameSite=Lax; ${clear ? 'Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:01 GMT' : `Max-Age=86400; Expires=${new Date(Date.now() + 86400000).toUTCString()}`}${secure ? '; Secure' : ''}`;
      response.setHeader('Set-Cookie', [
        `vpn_access=${clear ? '' : `access-${generation}`}; ${attributes}`,
        `vpn_refresh=${clear ? '' : `refresh-${generation}`}; ${attributes}`,
      ]);
    };
    // Mirrors the important Go Origin/CSRF contract without a database or real credentials.
    if (origin && !origins.has(origin)) return fail(403, 'origin_forbidden');
    if (!origin && !['GET', 'HEAD', 'OPTIONS'].includes(request.method) && cookie)
      return fail(403, 'origin_required');
    if (origin) {
      response.setHeader('Access-Control-Allow-Origin', origin);
      response.setHeader('Access-Control-Allow-Credentials', 'true');
      response.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
      response.setHeader(
        'Access-Control-Allow-Headers',
        'Content-Type, Authorization, Idempotency-Key',
      );
      response.setHeader('Vary', 'Origin');
    }
    if (request.method === 'OPTIONS') {
      response.writeHead(204);
      return response.end();
    }
    if (pathname === '/api/v1/auth/login' || pathname === '/api/v1/auth/register') {
      const input = JSON.parse(body || '{}');
      if (input.password !== 'proxy-test-password')
        return fail(401, 'invalid_credentials', '邮箱或密码不正确');
      active = true;
      generation++;
      cookies();
      return json(pathname.endsWith('register') ? 201 : 200, { user: testUser });
    }
    if (pathname === '/api/v1/auth/refresh') {
      if (!active || !cookie.includes(`vpn_refresh=refresh-${generation}`)) {
        cookies(true);
        return fail(401, 'session_expired');
      }
      generation++;
      cookies();
      return json(200, { refreshed: true });
    }
    if (pathname === '/api/v1/auth/logout') {
      active = false;
      cookies(true);
      return json(200, { logged_out: true });
    }
    if (pathname === '/api/v1/auth/me' || pathname === '/api/v1/me/dashboard') {
      if (!active || !cookie.includes(`vpn_access=access-${generation}`))
        return fail(401, 'unauthorized');
      return json(200, {
        user: testUser,
        subscription: null,
        order_count: 0,
        entitlement_active: false,
        metering_connected: false,
        proxy_service_ready: false,
      });
    }
    if (pathname === '/api/v1/orders')
      return json(200, { orders: [], total: 0, page: 1, page_size: 20 });
    if (pathname === '/api/v1/plans') return json(200, { plans: [] });
    if (pathname === '/api/v1/meta')
      return json(200, { upstream: label, test_purchase_enabled: false });
    if (pathname === '/api/v1/oversized-known') {
      response.writeHead(200, { 'Content-Length': (512 << 10) + 1 });
      return response.end(Buffer.alloc((512 << 10) + 1));
    }
    if (pathname === '/api/v1/oversized-chunked') {
      response.writeHead(200, { 'Content-Type': 'application/json' });
      response.flushHeaders();
      response.write(Buffer.alloc(512 << 10));
      return response.end('x');
    }
    if (pathname === '/api/v1/client/config') {
      return json(200, { yaml: 'x'.repeat(600 << 10) });
    }
    if (pathname === '/api/v1/slow') {
      await delay(1500);
      return json(200, { delayed: true });
    }
    if (pathname === '/api/v1/redirect') {
      response.writeHead(302, { Location: '/api/v1/redirect-target' });
      return response.end();
    }
    if (pathname === '/api/v1/rate-limit') {
      response.setHeader('Retry-After', '60');
      response.setHeader('X-Request-ID', 'proxy-test-request');
      return fail(429, 'rate_limited');
    }
    if (pathname === '/api/v1/cookie-security') cookies(false, true);
    if (pathname === '/api/v1/no-content') {
      response.writeHead(204);
      return response.end();
    }
    return json(200, record);
  });
  return { server, received };
}

test(
  'production same-origin API proxy (isolated, no real database)',
  { timeout: 240000 },
  async (t) => {
    await access(path.join(root, '.next/BUILD_ID'));
    const fingerprint = await staticFingerprint(path.join(root, '.next/static'));
    const stage = await mkdtemp(path.join(tmpdir(), 'vpn-api-proxy-'));
    const origins = new Set();
    const first = mockBackend('default-loopback', origins);
    const second = mockBackend('runtime-override', origins);
    let child;
    let browser;
    let nextURL;
    let browserURL;
    let logs = '';
    const cleanEnv = { ...process.env, NODE_ENV: 'production', NEXT_TELEMETRY_DISABLED: '1' };
    for (const name of [
      'API_INTERNAL_URL',
      'API_BASE_URL',
      'API_TIMEOUT_MS',
      'NEXT_PUBLIC_API_URL',
      'NEXT_PUBLIC_API_TIMEOUT_MS',
      '__NEXT_PROCESSED_ENV',
    ])
      delete cleanEnv[name];
    async function stopNext() {
      if (!child) return;
      if (child.exitCode === null && child.signalCode === null) {
        const exited = once(child, 'exit');
        child.kill('SIGTERM');
        const kill = setTimeout(() => child.kill('SIGKILL'), 5000);
        try {
          await exited;
        } finally {
          clearTimeout(kill);
        }
      }
      child = undefined;
    }
    async function startNext(extra = '') {
      await stopNext();
      // Never load or modify the real frontend/.env. Old public URLs must be ignored.
      await writeFile(
        path.join(stage, '.env'),
        `NEXT_PUBLIC_API_URL=http://localhost:9/api/v1\nAPI_BASE_URL=http://localhost:9/api/v1\nNEXT_PUBLIC_API_TIMEOUT_MS=65000\n${extra}`,
      );
      const port = await freePort();
      nextURL = `http://127.0.0.1:${port}`;
      browserURL = `http://vpn-proxy.example.test:${port}`;
      origins.add(browserURL);
      origins.add(nextURL);
      logs = '';
      child = spawn(
        process.execPath,
        [
          path.join(root, 'node_modules/next/dist/bin/next'),
          'start',
          '--hostname',
          '127.0.0.1',
          '--port',
          String(port),
        ],
        { cwd: stage, env: cleanEnv, stdio: ['ignore', 'pipe', 'pipe'] },
      );
      const collect = (chunk) => {
        logs = (logs + chunk.toString()).slice(-8000);
      };
      child.stdout.on('data', collect);
      child.stderr.on('data', collect);
      for (let attempt = 0; attempt < 150; attempt++) {
        if (child.exitCode !== null) throw new Error(`Next.js exited before readiness: ${logs}`);
        try {
          const response = await fetch(`${nextURL}/api/runtime-config`, {
            signal: AbortSignal.timeout(1000),
          });
          await response.arrayBuffer();
          return;
        } catch {
          await delay(100);
        }
      }
      throw new Error(`Next.js did not start: ${logs}`);
    }
    async function api(route, init) {
      return fetch(nextURL + '/api/v1' + route, { ...init, signal: AbortSignal.timeout(10000) });
    }
    try {
      for (const filename of ['package.json', 'next.config.ts', 'tsconfig.json'])
        await copyFile(path.join(root, filename), path.join(stage, filename));
      await symlink(path.join(root, 'node_modules'), path.join(stage, 'node_modules'), 'dir');
      await symlink(path.join(root, '.next'), path.join(stage, '.next'), 'dir');
      // Refuse to run if 8080 is occupied; never send these requests to a real backend.
      await listen(first.server, 8080);
      const secondPort = await listen(second.server);
      await startNext('API_TIMEOUT_MS=1000\n');

      await t.test(
        'default loopback upstream; legacy public URL ignored; only relative URL exposed',
        async () => {
          const response = await fetch(nextURL + '/api/runtime-config');
          assert.equal(response.status, 200);
          assert.equal(response.headers.get('cache-control'), 'no-store');
          assert.deepEqual(await response.json(), { apiBaseUrl: '/api/v1', apiTimeoutMs: 6000 });
          assert.equal((await (await api('/meta')).json()).upstream, 'default-loopback');
        },
      );

      await t.test(
        'browser login, persisted cookies, automatic refresh, orders and logout on a non-localhost origin',
        async () => {
          browser = await chromium.launch({
            ...(process.env.PLAYWRIGHT_CHROME_PATH
              ? { executablePath: process.env.PLAYWRIGHT_CHROME_PATH }
              : {}),
            args: [
              '--host-resolver-rules=MAP vpn-proxy.example.test 127.0.0.1',
              '--no-proxy-server',
            ],
          });
          const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
          const page = await context.newPage();
          const errors = [];
          const consoleErrors = [];
          const requests = [];
          page.on('pageerror', (error) => errors.push(error.message));
          page.on('console', (message) => {
            if (message.type() === 'error')
              consoleErrors.push({ text: message.text(), url: message.location().url });
          });
          page.on('request', (request) => requests.push(request.url()));
          const initialRefresh = page.waitForResponse((response) =>
            response.url().endsWith('/api/v1/auth/refresh'),
          );
          await page.goto(browserURL + '/login');
          await initialRefresh;
          assert.match(await page.title(), /AsterLink/);
          assert.equal(await page.getByRole('heading', { name: '欢迎回来' }).isVisible(), true);
          await page.getByLabel('邮箱地址').fill(testUser.email);
          await page.locator('input[type="password"]').fill('wrong-password');
          await page.getByRole('button', { name: '登录', exact: true }).click();
          await page.getByText('邮箱或密码不正确').waitFor();
          await page.locator('input[type="password"]').fill('proxy-test-password');
          await page.getByLabel('记住登录状态（30 天）').check();
          await page.getByRole('button', { name: '登录', exact: true }).click();
          await page.waitForURL('**/dashboard');
          await page.getByRole('heading', { name: '还没有开通套餐' }).waitFor();
          const cookies = (await context.cookies(browserURL + '/api/v1')).filter((cookie) =>
            cookie.name.startsWith('vpn_'),
          );
          assert.equal(cookies.length, 2);
          assert.ok(
            cookies.every(
              (cookie) =>
                cookie.httpOnly &&
                cookie.sameSite === 'Lax' &&
                cookie.domain === 'vpn-proxy.example.test' &&
                cookie.path === '/api/v1' &&
                cookie.expires > Date.now() / 1000,
            ),
          );
          assert.equal(await page.evaluate(() => localStorage.length), 0);
          await page.reload();
          await page.getByRole('heading', { name: '还没有开通套餐' }).waitFor();
          await context.clearCookies({ name: 'vpn_access' });
          const refresh = page.waitForResponse(
            (response) =>
              response.url().endsWith('/api/v1/auth/refresh') && response.status() === 200,
          );
          await page.reload();
          await refresh;
          await page.getByRole('heading', { name: '还没有开通套餐' }).waitFor();
          assert.ok(
            first.received.some(
              (record) =>
                record.url === '/api/v1/auth/refresh' &&
                record.headers.cookie?.includes('vpn_refresh=refresh-1') &&
                record.headers.origin === browserURL,
            ),
          );
          await page.getByRole('link', { name: '订单记录', exact: true }).click();
          await page.getByRole('heading', { name: '还没有订单' }).waitFor();
          await page.getByRole('button', { name: '账户菜单' }).click();
          await page.getByRole('menuitem', { name: '退出登录', exact: true }).click();
          await page.waitForURL('**/login');
          assert.equal(
            (await context.cookies(browserURL + '/api/v1')).filter((cookie) =>
              cookie.name.startsWith('vpn_'),
            ).length,
            0,
          );
          await page.setViewportSize({ width: 390, height: 844 });
          assert.equal(await page.getByRole('heading', { name: '欢迎回来' }).isVisible(), true);
          assert.ok(
            await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
          );
          assert.ok((await page.screenshot()).length > 1000);
          assert.deepEqual(errors, []);
          assert.deepEqual(
            consoleErrors.filter(
              (error) => !/\b401\b/.test(error.text) && !error.url?.endsWith('/favicon.ico'),
            ),
            [],
          );
          assert.ok(requests.some((url) => url === browserURL + '/api/v1/auth/login'));
          assert.ok(
            requests
              .filter((url) => /^https?:/.test(url))
              .every((url) => new URL(url).origin === browserURL),
          );
          await context.close();
          await browser.close();
          browser = undefined;
        },
      );

      await t.test(
        'method, query, JSON, cookies, bearer and idempotency headers are preserved',
        async () => {
          for (const method of ['POST', 'PUT', 'PATCH', 'DELETE']) {
            const response = await api('/echo?page=2&tag=a&tag=b&q=%E4%B8%AD', {
              method,
              headers: {
                Origin: browserURL,
                'Content-Type': 'application/json',
                Cookie: 'vpn_access=test-only',
                Authorization: 'Bearer test-only',
                'Idempotency-Key': 'test-key',
                'X-Forwarded-For': '203.0.113.7',
              },
              body: '{"plan_id":"pro"}',
            });
            const record = await response.json();
            assert.equal(record.method, method);
            assert.equal(record.url, '/api/v1/echo?page=2&tag=a&tag=b&q=%E4%B8%AD');
            assert.equal(record.body, '{"plan_id":"pro"}');
            assert.equal(record.headers.origin, browserURL);
            assert.equal(record.headers.cookie, 'vpn_access=test-only');
            assert.equal(record.headers.authorization, 'Bearer test-only');
            assert.equal(record.headers['idempotency-key'], 'test-key');
            assert.equal(record.headers['x-forwarded-for'], undefined);
            assert.equal(record.headers.host, '127.0.0.1:8080');
          }
          assert.equal(await (await api('/echo', { method: 'HEAD' })).text(), '');
          assert.equal((await api('/no-content')).status, 204);
          const limited = await api('/rate-limit');
          assert.equal(limited.status, 429);
          assert.equal(limited.headers.get('retry-after'), '60');
          assert.equal(limited.headers.get('x-request-id'), 'proxy-test-request');
          assert.equal((await limited.json()).error.code, 'rate_limited');
        },
      );

      await t.test(
        'Origin/CSRF checks and both secure Set-Cookie headers survive the proxy',
        async () => {
          const rejected = await api('/echo', {
            method: 'POST',
            headers: { Origin: 'https://evil.example.invalid', 'Content-Type': 'application/json' },
            body: '{}',
          });
          assert.equal(rejected.status, 403);
          assert.equal((await rejected.json()).error.code, 'origin_forbidden');
          const missing = await api('/echo', {
            method: 'POST',
            headers: { Cookie: 'vpn_access=test-only', 'Content-Type': 'application/json' },
            body: '{}',
          });
          assert.equal((await missing.json()).error.code, 'origin_required');
          const preflight = await api('/echo', {
            method: 'OPTIONS',
            headers: {
              Origin: browserURL,
              'Access-Control-Request-Method': 'POST',
              'Access-Control-Request-Headers': 'Content-Type',
            },
          });
          assert.equal(preflight.status, 204);
          assert.equal(preflight.headers.get('access-control-allow-origin'), browserURL);
          const secured = await api('/cookie-security');
          const cookies = secured.headers.getSetCookie();
          assert.equal(cookies.length, 2);
          assert.ok(
            cookies.every(
              (cookie) =>
                cookie.includes('Secure') &&
                cookie.includes('HttpOnly') &&
                cookie.includes('Expires=') &&
                cookie.includes('Path=/api/v1'),
            ),
          );
        },
      );

      await t.test(
        'unsafe paths, oversized bodies and upstream redirects are blocked',
        async () => {
          assert.equal((await api('/auth%2Flogin')).status, 400);
          assert.equal((await api('/%252e%252e/healthz')).status, 400);
          const before = first.received.length;
          assert.equal(
            (await api('/echo', { method: 'POST', body: 'x'.repeat(8193) })).status,
            413,
          );
          const stream = new ReadableStream({
            start(controller) {
              controller.enqueue(new Uint8Array(8193));
              controller.close();
            },
          });
          assert.equal(
            (await api('/echo', { method: 'POST', body: stream, duplex: 'half' })).status,
            413,
          );
          assert.equal(first.received.length, before);
          const redirect = await api('/redirect');
          assert.equal(redirect.status, 502);
          assert.equal((await redirect.json()).error.code, 'upstream_redirect');
          assert.equal(redirect.headers.get('location'), null);
          assert.ok(!first.received.some((record) => record.url === '/api/v1/redirect-target'));
        },
      );

      await t.test('upstream response limits include chunked bodies and preserve large client profiles', async () => {
        for (const route of ['/oversized-known', '/oversized-chunked']) {
          const response = await api(route);
          assert.equal(response.status, 502);
          assert.equal((await response.json()).error.code, 'upstream_response_too_large');
        }
        const profile = await api('/client/config');
        assert.equal(profile.status, 200);
        assert.equal((await profile.json()).yaml.length, 600 << 10);
        const imported = await api('/admin/nodes', {
          method: 'POST',
          headers: { Origin: browserURL, 'Content-Type': 'application/json' },
          body: JSON.stringify({ yaml: 'x'.repeat(64 << 10) }),
        });
        assert.equal(imported.status, 200);
      });

      await t.test('upstream timeouts return a controlled 504', async () => {
        const response = await api('/slow');
        assert.equal(response.status, 504);
        assert.equal((await response.json()).error.code, 'upstream_timeout');
      });

      await t.test(
        'same build switches upstream and timeout after changing only isolated .env and restarting',
        async () => {
          await startNext(
            `API_INTERNAL_URL=http://127.0.0.1:${secondPort}/api/v1/\nAPI_TIMEOUT_MS=1500\n`,
          );
          assert.equal((await (await api('/meta')).json()).upstream, 'runtime-override');
          assert.deepEqual(await (await fetch(nextURL + '/api/runtime-config')).json(), {
            apiBaseUrl: '/api/v1',
            apiTimeoutMs: 6500,
          });
          assert.equal(
            (await (await fetch('http://127.0.0.1:8080/api/v1/meta')).json()).upstream,
            'default-loopback',
          );
        },
      );

      await t.test(
        'invalid runtime configuration fails closed without exposing the internal address',
        async () => {
          for (const configuration of [
            'API_INTERNAL_URL=/api/v1\n',
            'API_INTERNAL_URL=http://user:password@127.0.0.1:8080/api/v1\n',
            'API_INTERNAL_URL=http://127.0.0.1:8080/api/v1?bad=1\n',
            'API_TIMEOUT_MS=not-a-number\n',
          ]) {
            await startNext(configuration);
            const response = await fetch(nextURL + '/api/runtime-config');
            assert.equal(response.status, 503);
            assert.equal((await response.json()).error.code, 'api_config_invalid');
            const failed = await api('/meta');
            assert.equal(failed.status, 503);
            assert.doesNotMatch(await failed.text(), /127\.0\.0\.1|password@/);
          }
        },
      );

      await t.test(
        'unavailable upstream is a controlled 502; no localhost fallback in the browser',
        async () => {
          const unavailablePort = await freePort();
          await startNext(`API_INTERNAL_URL=http://127.0.0.1:${unavailablePort}/api/v1\n`);
          const response = await api('/meta');
          assert.equal(response.status, 502);
          assert.equal((await response.json()).error.code, 'upstream_unavailable');
        },
      );

      await t.test('all deployment scenarios reuse identical browser bundles', async () => {
        assert.equal(await staticFingerprint(path.join(root, '.next/static')), fingerprint);
      });
    } finally {
      await browser?.close();
      await stopNext();
      await close(first.server);
      await close(second.server);
      await rm(stage, { recursive: true, force: true });
    }
  },
);
