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
// Synthetic reset token (43 URL-safe characters, like the Go token); never a real secret.
const resetToken = 'ProxyTestResetTokenProxyTestResetToken12345';
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
  const settings = { registrationOpen: true, resetUsed: false, purchase: false };
  let codeRequests = 0;
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
    if (pathname === '/api/v1/auth/login') {
      const input = JSON.parse(body || '{}');
      if (input.password !== 'proxy-test-password')
        return fail(401, 'invalid_credentials', '邮箱或密码不正确');
      active = true;
      generation++;
      cookies();
      return json(200, { user: testUser });
    }
    // Registration mirrors the Go contract: code request, then a proof-bound final step.
    if (pathname === '/api/v1/auth/register-code') {
      const { email } = JSON.parse(body || '{}');
      if (!settings.registrationOpen) return fail(403, 'registration_disabled', '当前暂未开放注册');
      if (email === 'limited@qq.com') {
        response.setHeader('Retry-After', '75');
        return fail(429, 'rate_limited', '请求过于频繁，请稍后重试');
      }
      if (email === 'down@qq.com')
        return fail(503, 'mail_unavailable', '邮件服务暂时不可用，请稍后重试');
      if (email === 'slow@qq.com') await delay(1500);
      codeRequests++;
      response.setHeader('X-Request-ID', 'register-code-' + codeRequests);
      return json(202, {
        message: '如果该邮箱可以注册，6 位验证码会在几分钟内送达，请同时检查垃圾邮件箱。',
        verification_id: `verification-${codeRequests}-${email}`,
        expires_at: new Date(Date.now() + 600000).toISOString(),
        next_send_at: new Date(Date.now() + 60000).toISOString(),
        expires_in_seconds: 600,
        resend_after_seconds: 60,
      });
    }
    if (pathname === '/api/v1/auth/register') {
      const input = JSON.parse(body || '{}');
      if (!settings.registrationOpen) return fail(403, 'registration_disabled', '当前暂未开放注册');
      if (!input.verification_id)
        return fail(400, 'verification_required', '请先获取并填写邮箱验证码');
      if (input.code !== '012345')
        return fail(400, 'verification_invalid', '验证码不正确，请检查后重新输入');
      active = true;
      generation++;
      cookies();
      return json(201, { user: { ...testUser, email: input.email, name: input.name } });
    }
    // Password recovery mirrors the Go contract; a successful reset revokes every session.
    if (pathname === '/api/v1/auth/forgot-password') {
      const { email } = JSON.parse(body || '{}');
      if (email === 'limited@qq.com') {
        response.setHeader('Retry-After', '75');
        return fail(429, 'rate_limited', '请求过于频繁，请稍后重试');
      }
      if (email === 'down@qq.com')
        return fail(503, 'mail_unavailable', '邮件服务暂时不可用，请稍后重试');
      return json(202, {
        message: '如果该邮箱已注册，重置密码的链接会在几分钟内送达，请同时检查垃圾邮件箱。',
        next_send_at: new Date(Date.now() + 60000).toISOString(),
        resend_after_seconds: 60,
      });
    }
    if (
      pathname === '/api/v1/auth/reset-password/validate' ||
      pathname === '/api/v1/auth/reset-password'
    ) {
      const input = JSON.parse(body || '{}');
      if (input.token !== resetToken || settings.resetUsed)
        return fail(400, 'reset_token_invalid', '重置链接无效或已过期，请重新申请找回密码');
      if (pathname.endsWith('/validate'))
        return json(200, {
          valid: true,
          expires_at: new Date(Date.now() + 1800000).toISOString(),
          expires_in_seconds: 1800,
          password_min_bytes: 10,
          password_max_bytes: 72,
        });
      if (input.password !== input.password_confirmation)
        return fail(400, 'password_mismatch', '两次输入的新密码不一致');
      if (input.password === 'proxy-test-password')
        return fail(400, 'password_unchanged', '新密码不能与当前密码相同');
      settings.resetUsed = true;
      active = false;
      cookies(true);
      return json(200, { password_reset: true, message: '密码已重置' });
    }
    if (pathname === '/api/v1/orders/test-purchase') {
      if (!active || !cookie.includes(`vpn_access=access-${generation}`))
        return fail(401, 'unauthorized');
      return json(200, { order: { id: 'order-1' }, replayed: false });
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
    if (pathname === '/api/v1/me/devices') {
      if (!active || !cookie.includes(`vpn_access=access-${generation}`))
        return fail(401, 'unauthorized');
      return json(200, {
        server_time: new Date().toISOString(),
        device_limit: 2,
        online_count: 0,
        online: [],
        signed_in: [],
        lease_seconds: 90,
      });
    }
    if (pathname === '/api/v1/me/usage-history') {
      if (!active || !cookie.includes(`vpn_access=access-${generation}`))
        return fail(401, 'unauthorized');
      const now = Date.now();
      const buckets = Array.from({ length: 8 }, (_, index) => ({
        start: new Date(now - (8 - index) * 10800000).toISOString(),
        end: new Date(now - (7 - index) * 10800000).toISOString(),
        upload_bytes: 0,
        download_bytes: 0,
        charged_units: 0,
      }));
      return json(200, {
        server_time: new Date(now).toISOString(),
        bucket_seconds: 10800,
        granularity_seconds: 300,
        buckets,
        total_upload_bytes: 0,
        total_download_bytes: 0,
        total_charged_units: 0,
      });
    }
    if (pathname === '/api/v1/orders') {
      if (!active || !cookie.includes(`vpn_access=access-${generation}`))
        return fail(401, 'unauthorized');
      return json(200, { orders: [], total: 0, page: 1, page_size: 20 });
    }
    if (pathname === '/api/v1/plans')
      return json(200, {
        plans: settings.purchase
          ? [
              {
                id: 'pro',
                name: '代理测试套餐',
                description: '仅用于代理测试',
                price_cents: 100,
                duration_days: 30,
                traffic_bytes: 1073741824,
                max_devices: 2,
                allow_dedicated: false,
              },
            ]
          : [],
      });
    if (pathname === '/api/v1/meta')
      return json(200, {
        upstream: label,
        test_purchase_enabled: settings.purchase,
        allow_registration: settings.registrationOpen,
        registration_email_domains: ['qq.com', 'gmail.com'],
        password_reset_enabled: true,
        verification_code_ttl_seconds: 600,
        verification_resend_after_seconds: 60,
      });
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
  return { server, received, settings };
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
      'CLIENT_IP_HEADER',
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
        'registration: email code request, cooldown, stale responses, field errors and closed state',
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
          page.on('pageerror', (error) => errors.push(error.message));
          const codeRequests = () =>
            first.received.filter((record) => record.url === '/api/v1/auth/register-code');
          const sendButton = () =>
            page.getByRole('button', { name: /获取验证码|重新发送|秒后可重发|发送中/ });
          await page.goto(browserURL + '/');
          await page.getByTestId('hero-actions').getByRole('link', { name: '创建账户' }).click();
          await page.waitForURL('**/register');
          await page.getByRole('heading', { name: '创建你的账户' }).waitFor();
          await page.getByText('支持：qq.com、gmail.com').waitFor();

          // The published allow-list gives immediate feedback; nothing is sent.
          await page.getByLabel('姓名 / 昵称').fill('注册测试');
          await page.getByLabel('邮箱地址').fill('someone@example.com');
          await sendButton().click();
          await page.getByText('该邮箱服务商暂不支持注册').waitFor();
          assert.equal(codeRequests().length, 0);

          // A slow answer for an address the user has since changed must not apply.
          await page.getByLabel('邮箱地址').fill('slow@qq.com');
          await sendButton().click();
          await page.getByLabel('邮箱地址').fill('fresh@qq.com');
          await delay(2000);
          assert.equal(codeRequests().length, 1);
          assert.equal(await page.getByRole('status').count(), 0);
          assert.equal(await sendButton().textContent(), '获取验证码');

          await sendButton().click();
          await page.getByText('如果该邮箱可以注册').waitFor();
          await page.getByText(/验证码 \d+:\d\d 内有效/).waitFor();
          assert.match(await sendButton().textContent(), /^\d+ 秒后可重发$/);
          assert.equal(await sendButton().isDisabled(), true);

          // Only ASCII digits reach the field; the byte rule is explained, not a character rule.
          const codeField = page.getByLabel('邮箱验证码');
          assert.equal(await codeField.getAttribute('inputmode'), 'numeric');
          assert.equal(await codeField.getAttribute('autocomplete'), 'one-time-code');
          await codeField.fill('９９ 99-99x99');
          assert.equal(await codeField.inputValue(), '999999');
          await page.locator('input[type="password"]').fill('短密码');
          await page.getByRole('button', { name: '创建账户' }).click();
          await page.getByText('密码需要 10–72 位（当前 9 位）').waitFor();
          await page.locator('input[type="password"]').fill('安全密码');
          await page.getByRole('button', { name: '创建账户' }).click();
          await page.getByText('验证码不正确，请检查后重新输入').waitFor();
          assert.equal(await page.getByLabel('姓名 / 昵称').inputValue(), '注册测试');
          assert.equal(await page.getByLabel('邮箱地址').inputValue(), 'fresh@qq.com');

          await codeField.fill('012345');
          await page.getByRole('button', { name: '创建账户' }).click();
          await page.waitForURL('**/dashboard');
          const submitted = first.received.filter((r) => r.url === '/api/v1/auth/register').at(-1);
          assert.deepEqual(Object.keys(JSON.parse(submitted.body)).sort(), [
            'code',
            'email',
            'name',
            'password',
            'remember',
            'verification_id',
          ]);
          assert.equal(JSON.parse(submitted.body).verification_id, 'verification-2-fresh@qq.com');
          assert.equal(JSON.parse(submitted.body).password, '安全密码');
          assert.equal(
            (await context.cookies(browserURL + '/api/v1')).filter((c) => c.name.startsWith('vpn_'))
              .length,
            2,
          );
          assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0);
          await context.clearCookies();

          // Server cooldown (429 + Retry-After) and mail outages keep the form intact.
          const second = await context.newPage();
          await second.goto(browserURL + '/register');
          await second.getByLabel('邮箱地址').fill('limited@qq.com');
          await second.getByRole('button', { name: '获取验证码' }).click();
          await second.getByText('发送过于频繁，请在 1:15 后重试。').waitFor();
          const limited = second.getByRole('button', { name: /秒后可重发/ });
          assert.match(await limited.textContent(), /^7[45] 秒后可重发$/);
          assert.equal(await limited.isDisabled(), true);
          await second.getByLabel('邮箱地址').fill('down@qq.com');
          await second.getByRole('button', { name: '获取验证码' }).click();
          await second.getByText('邮件服务暂时不可用，请稍后重试').waitFor();
          // A slow upstream (beyond API_TIMEOUT_MS) surfaces as the proxy's controlled 504.
          await second.getByLabel('邮箱地址').fill('slow@qq.com');
          await second.getByRole('button', { name: '获取验证码' }).click();
          await second.getByText('服务响应超时，请稍后重试。').waitFor();
          assert.equal(await second.getByLabel('邮箱地址').inputValue(), 'slow@qq.com');
          await second.setViewportSize({ width: 390, height: 844 });
          assert.ok(
            await second.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
          );

          // Closed registration: the page, the landing action and the login link agree.
          first.settings.registrationOpen = false;
          try {
            await second.goto(browserURL + '/register');
            await second.getByText('当前暂未开放注册').waitFor();
            assert.equal(await second.getByLabel('邮箱验证码').count(), 0);
            await second.goto(browserURL + '/');
            await second
              .getByTestId('hero-actions')
              .getByRole('link', { name: '登录账户' })
              .waitFor();
            assert.equal(await second.getByRole('link', { name: '创建账户' }).count(), 0);
            await second.goto(browserURL + '/login');
            await second.getByRole('heading', { name: '欢迎回来' }).waitFor();
            await delay(500);
            assert.equal(await second.getByRole('link', { name: '创建账户' }).count(), 0);
          } finally {
            first.settings.registrationOpen = true;
          }
          assert.deepEqual(errors, []);
          await context.close();
          await browser.close();
          browser = undefined;
        },
      );

      await t.test(
        'password reset: forgot form, fragment token, rules, revocation and final 401 in other tabs',
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
          first.settings.purchase = true;
          try {
            // A signed-in browser with a dashboard tab and an open purchase confirmation. It uses
            // the loopback origin: the purchase dialog needs a secure context (crypto.randomUUID).
            const signedIn = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
            const errors = [];
            const dashboard = await signedIn.newPage();
            dashboard.on('pageerror', (error) => errors.push(error.message));
            await dashboard.goto(nextURL + '/login');
            await dashboard.getByLabel('邮箱地址').fill(testUser.email);
            await dashboard.locator('input[type="password"]').fill('proxy-test-password');
            await dashboard.getByRole('button', { name: '登录', exact: true }).click();
            await dashboard.waitForURL('**/dashboard');
            await dashboard.getByRole('heading', { name: '还没有开通套餐' }).waitFor();
            const plans = await signedIn.newPage();
            plans.on('pageerror', (error) => errors.push(error.message));
            await plans.goto(nextURL + '/plans');
            await plans.getByRole('button', { name: '账户菜单' }).waitFor();
            await plans.getByRole('button', { name: '测试购买 代理测试套餐' }).click();
            await plans.getByRole('dialog', { name: '确认测试购买' }).waitFor();

            // Another browser (the mail opened on a phone, say) asks for a link.
            const mailbox = await browser.newContext({ viewport: { width: 390, height: 844 } });
            const page = await mailbox.newPage();
            page.on('pageerror', (error) => errors.push(error.message));
            const requests = [];
            page.on('request', (request) =>
              requests.push({ url: request.url(), body: request.postData() || '' }),
            );
            await page.goto(browserURL + '/login');
            await page.getByRole('link', { name: '忘记密码？' }).click();
            await page.waitForURL('**/forgot-password');
            await page.getByRole('heading', { name: '找回密码' }).waitFor();
            const send = () =>
              page.getByRole('button', { name: /发送重置链接|重新发送|秒后可重发|发送中/ });
            await page.getByLabel('邮箱地址').fill('not-an-address');
            await send().click();
            await page.getByText('请输入有效的邮箱地址').waitFor();
            await page.getByLabel('邮箱地址').fill('down@qq.com');
            await send().click();
            await page.getByText('邮件服务暂时不可用，请稍后重试').waitFor();
            await page.getByLabel('邮箱地址').fill('limited@qq.com');
            await send().click();
            await page.getByText('请求过于频繁，请在 1:15 后重试。').waitFor();
            assert.match(await send().textContent(), /^7[45] 秒后可重发$/);
            assert.equal(await send().isDisabled(), true);
            await page.getByLabel('邮箱地址').fill(testUser.email);
            await send().click();
            await page.getByText('如果该邮箱已注册').waitFor();
            assert.match(await send().textContent(), /^\d+ 秒后可重发$/);
            assert.ok(
              await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
            );

            // Opening a page without the fragment, or with a bad token, explains what to do.
            await page.goto(browserURL + '/reset-password');
            await page.getByText('请从邮件中打开重置链接').waitFor();
            await page.goto(browserURL + '/reset-password#token=not-a-real-token');
            await page.getByText('重置链接无效或已过期').waitFor();
            assert.equal(new URL(page.url()).hash, '');

            // The real link: the token leaves the address bar at once and only travels in POST bodies.
            // (The bad-token step above arrived as a fragment-only change of the open page.)
            const { headers } = await fetch(nextURL + '/reset-password');
            assert.equal(headers.get('referrer-policy'), 'no-referrer');
            assert.match(headers.get('x-robots-tag'), /noindex/);
            assert.match(headers.get('cache-control'), /no-store/);
            await page.goto(browserURL + '/forgot-password');
            await page.goto(browserURL + '/reset-password#token=' + resetToken);
            await page.getByLabel(/^新密码/).waitFor();
            assert.equal(new URL(page.url()).hash, '');
            assert.equal(
              await page.evaluate(() =>
                document.querySelector('meta[name="referrer"]')?.getAttribute('content'),
              ),
              'no-referrer',
            );
            await page.getByLabel(/^新密码/).fill('短密码');
            await page.getByLabel('确认新密码').fill('短密码');
            await page.getByRole('button', { name: '设置新密码' }).click();
            await page.getByText('密码需要 10–72 位（当前 9 位）').waitFor();
            await page.getByLabel(/^新密码/).fill('新的安全密码');
            await page.getByLabel('确认新密码').fill('新的安全密码!');
            await page.getByRole('button', { name: '设置新密码' }).click();
            await page.getByText('两次输入的新密码不一致').waitFor();
            await page.getByLabel(/^新密码/).fill('proxy-test-password');
            await page.getByLabel('确认新密码').fill('proxy-test-password');
            await page.getByRole('button', { name: '设置新密码' }).click();
            await page.getByText('新密码不能与当前密码相同').waitFor();
            await page.getByLabel(/^新密码/).fill('新的安全密码');
            await page.getByLabel('确认新密码').fill('新的安全密码');
            await page.getByRole('button', { name: '设置新密码' }).click();
            await page.getByText('密码已重置').waitFor();
            await page.getByRole('link', { name: '使用新密码登录' }).waitFor();
            const submitted = first.received
              .filter((r) => r.url === '/api/v1/auth/reset-password')
              .at(-1);
            assert.deepEqual(Object.keys(JSON.parse(submitted.body)).sort(), [
              'password',
              'password_confirmation',
              'token',
            ]);
            assert.ok(requests.every((r) => !r.url.includes(resetToken)));
            assert.ok(
              requests
                .filter((r) => r.body.includes(resetToken))
                .every((r) => /\/api\/v1\/auth\/reset-password(\/validate)?$/.test(r.url)),
            );
            assert.ok(
              requests
                .filter((r) => /^https?:/.test(r.url))
                .every((r) => new URL(r.url).origin === browserURL),
            );
            assert.ok(first.received.every((r) => !r.url.includes(resetToken)));
            assert.equal(await page.evaluate(() => localStorage.length + sessionStorage.length), 0);
            // The used link is dead.
            await page.goto(browserURL + '/reset-password#token=' + resetToken);
            await page.getByText('重置链接无效或已过期').waitFor();

            // The other browser learns it on its next protected request: the dialog closes and
            // the old dashboard is gone, without a reload.
            await plans.getByRole('button', { name: '确认模拟开通' }).click();
            await plans
              .getByRole('dialog', { name: '确认测试购买' })
              .waitFor({ state: 'detached' });
            await plans.getByRole('link', { name: '登录账户' }).waitFor();
            await dashboard.getByRole('link', { name: '订单记录', exact: true }).click();
            await dashboard.waitForURL('**/login');
            assert.equal(await dashboard.getByText('还没有开通套餐').count(), 0);
            assert.deepEqual(errors, []);
            await signedIn.close();
            await mailbox.close();
          } finally {
            first.settings.purchase = false;
            first.settings.resetUsed = false;
            await browser.close();
            browser = undefined;
          }
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
        'the browser User-Agent reaches the API, cut to 200 characters (login records)',
        async () => {
          const short = await (
            await api('/echo', { headers: { 'User-Agent': 'AccountTest/1.0 (probe)' } })
          ).json();
          assert.equal(short.headers['user-agent'], 'AccountTest/1.0 (probe)');
          const long = await (
            await api('/echo', { headers: { 'User-Agent': 'U'.repeat(500) } })
          ).json();
          assert.equal(long.headers['user-agent'], 'U'.repeat(200));
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
          // Announcement text (5000 characters, up to 20 KB) is the one body allowed past 8 KB.
          const announcements = '/admin/console/announcements';
          assert.notEqual(
            (await api(announcements, { method: 'POST', body: 'x'.repeat(20000) })).status,
            413,
          );
          assert.equal(first.received.length, before + 1);
          assert.equal(
            (await api(announcements, { method: 'POST', body: 'x'.repeat((32 << 10) + 1) })).status,
            413,
          );
          assert.equal(
            (await api('/admin/console/coupons', { method: 'POST', body: 'x'.repeat(8193) }))
              .status,
            413,
          );
          assert.equal(first.received.length, before + 1);
          const redirect = await api('/redirect');
          assert.equal(redirect.status, 502);
          assert.equal((await redirect.json()).error.code, 'upstream_redirect');
          assert.equal(redirect.headers.get('location'), null);
          assert.ok(!first.received.some((record) => record.url === '/api/v1/redirect-target'));
        },
      );

      await t.test(
        'upstream response limits include chunked bodies and preserve large client profiles',
        async () => {
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
        },
      );

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
        'client address: only the configured ingress header, as one IP, reaches Go',
        async () => {
          const echo = async (headers) =>
            (await (await api('/echo', { headers })).json()).headers['x-forwarded-for'];
          // Default: nothing is forwarded, whatever the browser sends.
          await startNext(`API_INTERNAL_URL=http://127.0.0.1:${secondPort}/api/v1\n`);
          assert.equal(
            await echo({ 'X-Real-IP': '203.0.113.9', 'X-Forwarded-For': '1.2.3.4' }),
            undefined,
          );
          await startNext(
            `API_INTERNAL_URL=http://127.0.0.1:${secondPort}/api/v1\nCLIENT_IP_HEADER=X-Real-IP\n`,
          );
          assert.equal(await echo({ 'X-Real-IP': '203.0.113.9' }), '203.0.113.9');
          assert.equal(await echo({ 'X-Real-IP': '2001:db8::7' }), '2001:db8::7');
          // Append-style chains: only the entry written by the ingress (the last) counts.
          assert.equal(await echo({ 'X-Real-IP': '1.2.3.4, 203.0.113.10' }), '203.0.113.10');
          // A client-supplied X-Forwarded-For never replaces the vouched address.
          assert.equal(
            await echo({ 'X-Real-IP': '203.0.113.11', 'X-Forwarded-For': '1.2.3.4' }),
            '203.0.113.11',
          );
          for (const bad of ['not-an-ip', '203.0.113.9:443', '203.0.113.9, garbage', ''])
            assert.equal(await echo({ 'X-Real-IP': bad, 'X-Forwarded-For': '1.2.3.4' }), undefined);
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
            'CLIENT_IP_HEADER=cookie\n',
            'CLIENT_IP_HEADER=x real ip\n',
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
