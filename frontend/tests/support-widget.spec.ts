import { expect, test, type BrowserContext, type Page, type Route } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';

// Online support chat acceptance (handoff_ui.md 9 and 11, round 4), on the payment fixture, which
// looks like production (CRISP_WEBSITE_ID unset, so the default website; SITE_SUPPORT_EMAIL set).
// The playwright configs make every Crisp host unresolvable; here request interception (which
// runs before name resolution) stands in for Crisp: a fake l.js that records each $crisp call in
// sessionStorage (it survives the reload after signing out), a request that never answers, or one
// that fails. supporta and supportb are ordinary users, supportroot an administrator.
type Fixture = {
  password: string;
  emails: { supporta: string; supportb: string; supportroot: string };
};

let fixture: Fixture;
test.beforeAll(async () => {
  const filename = process.env.PAYMENT_FIXTURE;
  if (!filename?.startsWith('/tmp/vpn-payment-'))
    throw new Error('Private payment fixture required');
  fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
});

const defaultWebsite = '83336a84-ae9f-4345-ba1f-583c0790002d';
const loader = 'https://client.crisp.chat/l.js';

const screens = () => `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
async function shot(page: Page, name: string) {
  await mkdir(screens(), { recursive: true });
  await page.screenshot({ path: `${screens()}/${name}.png`, animations: 'disabled' });
}

// The stand-in for Crisp: replays the queue, records every call, fires the ready callback.
const fakeCrisp = `(function () {
  function log(entry) {
    var all = JSON.parse(sessionStorage.getItem('__crisp') || '[]');
    all.push(entry);
    sessionStorage.setItem('__crisp', JSON.stringify(all));
  }
  var handlers = {};
  function run(command) {
    var parts = [];
    for (var i = 0; i < command.length; i++)
      if (typeof command[i] !== 'function')
        parts.push(Array.isArray(command[i]) ? command[i].join(',') : String(command[i]));
    if (command[0] === 'on') handlers[command[1]] = command[2];
    log(parts.join(' '));
    if (command[0] === 'do' && command[1] === 'chat:open' && handlers['chat:opened'])
      setTimeout(handlers['chat:opened'], 0);
  }
  var queue = window.$crisp || [];
  window.$crisp = { push: run, is: function () { return false; } };
  log('loaded ' + window.CRISP_WEBSITE_ID + ' ' + JSON.stringify(window.CRISP_RUNTIME_CONFIG));
  for (var i = 0; i < queue.length; i++) run(queue[i]);
  window.__crispFire = function (name, value) { if (handlers[name]) handlers[name](value); };
  setTimeout(function () { if (window.CRISP_READY_TRIGGER) window.CRISP_READY_TRIGGER(); }, 50);
})();`;

type Mode = 'ok' | 'hang' | 'fail';
// Every request to a Crisp host, answered according to the current mode; hung requests are kept
// so a test can answer them late.
async function crisp(context: BrowserContext, initial: Mode) {
  const state = {
    mode: initial,
    requests: [] as { url: string; at: number }[],
    held: [] as Route[],
  };
  await context.route(/^https:\/\/([a-z0-9-]+\.)*crisp\.chat\//, async (route) => {
    state.requests.push({ url: route.request().url(), at: Date.now() });
    if (state.mode === 'hang') state.held.push(route);
    else if (state.mode === 'fail') await route.abort('internetdisconnected');
    else if (route.request().url() === loader)
      await route.fulfill({ contentType: 'application/javascript', body: fakeCrisp });
    else await route.fulfill({ status: 204 });
  });
  return {
    state,
    scripts: () => state.requests.filter((r) => r.url === loader),
    // A late answer to the held requests.
    release: () =>
      Promise.all(
        state.held
          .splice(0)
          .map((route) =>
            route.fulfill({ contentType: 'application/javascript', body: fakeCrisp }),
          ),
      ),
  };
}

// Problems the page itself reports: uncaught exceptions and console errors and warnings.
function watch(page: Page) {
  const errors: string[] = [];
  const warnings: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => {
    // The browser's own "Failed to load resource" lines (401 when signed out, the unreachable
    // chat script) are not errors of the page.
    if (message.type() === 'error' && !message.text().startsWith('Failed to load resource'))
      errors.push(message.text());
    if (message.type() === 'warning') warnings.push(message.text());
  });
  return { errors, warnings };
}

async function login(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

const calls = (page: Page) =>
  page.evaluate(() => JSON.parse(sessionStorage.getItem('__crisp') || '[]') as string[]);
const launcher = (page: Page) => page.getByTestId('support-button');
const scriptElements = (page: Page) =>
  page.evaluate(() => document.querySelectorAll('script[src*="crisp.chat"]').length);
// The user closes the Crisp window (after Crisp has reported it open: that event is async).
async function closeChat(page: Page) {
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        setTimeout(() => {
          (window as unknown as { __crispFire: (n: string) => void }).__crispFire('chat:closed');
          resolve();
        }, 100),
      ),
  );
}
async function nav(page: Page, label: string, url: RegExp) {
  await page.getByRole('navigation').getByRole('link', { name: label, exact: true }).click();
  await expect(page).toHaveURL(url);
}

test('signed out: no launcher and no Crisp request on public pages; the FAQ points to the chat', async ({
  page,
  context,
}) => {
  const fake = await crisp(context, 'ok');
  for (const path of ['/', '/login', '/register', '/plans', '/download']) {
    await page.goto(path);
    await page.waitForLoadState('load');
    await expect(launcher(page)).toHaveCount(0);
  }
  await page.waitForTimeout(6500);
  expect(fake.state.requests).toEqual([]);
  await expect(page.getByTestId('support-nav')).toHaveCount(0);
  // The home page FAQ names the online chat, and the address next to it (SITE_SUPPORT_EMAIL).
  await page.goto('/#faq');
  await page.getByRole('button', { name: '如何联系客服？' }).click();
  const answer = page.getByRole('region', { name: '如何联系客服？' });
  await expect(answer).toContainText('在线客服');
  await expect(answer).toContainText('support@example.test');
  expect(fake.state.requests).toEqual([]);
});

test('signed in: launcher at once, Crisp after the page settles, one script, chat round trip, sign-out reset', async ({
  page,
  context,
}) => {
  test.setTimeout(180_000);
  const fake = await crisp(context, 'ok');
  const { errors } = watch(page);
  // The page's own API requests: none may still be running when Crisp is requested.
  const api: { url: string; start: number; end: number | null }[] = [];
  page.on('request', (request) => {
    if (request.url().includes('/api/'))
      api.push({ url: request.url(), start: Date.now(), end: null });
  });
  const finish = (request: { url: () => string }) => {
    const entry = api.findLast((e) => e.url === request.url() && e.end === null);
    if (entry) entry.end = Date.now();
  };
  page.on('requestfinished', finish);
  page.on('requestfailed', finish);

  await login(page, fixture.emails.supporta);
  const entered = Date.now();
  await expect(launcher(page)).toBeVisible({ timeout: 3000 });
  await expect(launcher(page)).toHaveAccessibleName('在线客服');
  expect(fake.state.requests).toEqual([]);
  await expect(page.getByTestId('support-nav')).toBeVisible();
  await shot(page, 'support-launcher-light');

  await expect.poll(() => fake.scripts().length, { timeout: 20000 }).toBe(1);
  const requested = fake.scripts()[0].at;
  expect(requested - entered).toBeGreaterThanOrEqual(3500);
  const running = api.filter((e) => e.start <= requested && (e.end === null || e.end > requested));
  expect(running.map((e) => e.url)).toEqual([]);
  await expect.poll(() => calls(page)).toContain('do chat:hide');
  expect(await calls(page)).toEqual([
    `loaded ${defaultWebsite} {"locale":"zh"}`,
    'safe true',
    'do chat:hide',
    'on chat:opened',
    'on chat:closed',
    'on message:received',
    `set user:email ${fixture.emails.supporta}`,
    'set user:nickname Pay supporta',
    'do chat:close',
    'do chat:hide',
  ]);
  await expect(page.locator('[data-testid="support-launcher"]')).toHaveAttribute(
    'data-status',
    'ready',
  );

  // Client navigation across the user pages: always one script, always the launcher.
  for (const [label, url] of [
    ['节点列表', /\/nodes$/],
    ['套餐与订阅', /\/plans$/],
    ['订单记录', /\/orders$/],
    ['下载客户端', /\/download$/],
    ['账号设置', /\/account$/],
    ['总览', /\/dashboard$/],
  ] as const) {
    await nav(page, label, url);
    await expect(launcher(page)).toBeVisible();
    expect(await scriptElements(page)).toBe(1);
  }
  expect(fake.scripts()).toHaveLength(1);

  // Open, close, unread.
  let mark = (await calls(page)).length;
  await launcher(page).click();
  await expect
    .poll(async () => (await calls(page)).slice(mark))
    .toEqual(['do chat:show', 'do chat:open']);
  await expect(launcher(page)).toHaveCount(0);
  await closeChat(page);
  await expect(launcher(page)).toBeVisible();
  expect((await calls(page)).at(-1)).toBe('do chat:hide');
  await page.evaluate(() =>
    (window as unknown as { __crispFire: (n: string, v: unknown) => void }).__crispFire(
      'message:received',
      { content: '你好' },
    ),
  );
  await expect(page.getByTestId('support-unread')).toBeVisible();
  await expect(launcher(page)).toHaveAccessibleName('在线客服，有新消息');
  await shot(page, 'support-launcher-unread');
  // The sidebar entry does the same and clears the dot.
  mark = (await calls(page)).length;
  await page.getByTestId('support-nav').click();
  await expect
    .poll(async () => (await calls(page)).slice(mark))
    .toEqual(['do chat:show', 'do chat:open']);
  await closeChat(page);
  await expect(launcher(page)).toBeVisible();
  await expect(page.getByTestId('support-unread')).toHaveCount(0);

  // Under the dialogs: a purchase dialog's overlay covers the launcher.
  await nav(page, '套餐与订阅', /\/plans$/);
  await page.getByRole('button', { name: '购买 轻量计划', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  const box = (await launcher(page).boundingBox())!;
  const covered = await page.evaluate(
    ([x, y]) => !document.elementFromPoint(x, y)?.closest('[data-testid="support-button"]'),
    [box.x + box.width / 2, box.y + box.height / 2],
  );
  expect(covered).toBe(true);
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);

  // Dark: the launcher follows the theme.
  await page.getByRole('button', { name: '切换外观' }).click();
  await page.getByRole('menuitem', { name: '深色' }).click();
  await nav(page, '总览', /\/dashboard$/);
  await shot(page, 'support-launcher-dark');
  await page.getByRole('button', { name: '切换外观' }).click();
  await page.getByRole('menuitem', { name: '浅色' }).click();

  // The administration (here as an ordinary user, by client navigation): no launcher, Crisp hidden.
  mark = (await calls(page)).length;
  await page.evaluate(() =>
    (window as unknown as { next: { router: { push: (p: string) => void } } }).next.router.push(
      '/admin/console/users',
    ),
  );
  await expect(page).toHaveURL(/\/admin\/console\/users$/);
  await expect(launcher(page)).toHaveCount(0);
  await expect(page.getByTestId('support-nav')).toHaveCount(0);
  expect((await calls(page)).slice(mark)).toEqual(['do chat:close', 'do chat:hide']);
  await page.goBack();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(launcher(page)).toBeVisible();

  // Signing out resets the conversation and reloads: no script is left for the sign-in page.
  mark = (await calls(page)).length;
  await page.getByRole('button', { name: '账户菜单' }).click();
  await page.getByRole('menuitem', { name: '退出登录' }).click();
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible();
  expect((await calls(page)).slice(mark)).toContain('do session:reset');
  expect(await scriptElements(page)).toBe(0);
  await expect(launcher(page)).toHaveCount(0);

  // The next account in this browser: the old conversation is reset before its own identity.
  mark = (await calls(page)).length;
  await login(page, fixture.emails.supportb);
  await expect.poll(() => fake.scripts().length, { timeout: 20000 }).toBe(2);
  await expect
    .poll(async () => (await calls(page)).slice(mark))
    .toContain(`set user:email ${fixture.emails.supportb}`);
  const next = (await calls(page)).slice(mark);
  expect(next.indexOf('do session:reset')).toBeGreaterThan(-1);
  expect(next.indexOf('do session:reset')).toBeLessThan(
    next.indexOf(`set user:email ${fixture.emails.supportb}`),
  );
  expect(next.join('\n')).not.toContain(fixture.emails.supporta);
  expect(errors).toEqual([]);
});

test('Crisp never answers: pages work, the launcher reports it after about 20 s, a late answer still opens', async ({
  page,
  context,
}) => {
  test.setTimeout(120_000);
  const fake = await crisp(context, 'hang');
  const { errors, warnings } = watch(page);
  await login(page, fixture.emails.supportb);
  // Clicked before the preload: requested at once, "connecting" shown.
  const clicked = Date.now();
  await launcher(page).click();
  await expect.poll(() => fake.scripts().length, { timeout: 2000 }).toBe(1);
  await expect(page.getByTestId('support-connecting')).toHaveText('正在连接客服…');
  await expect(launcher(page)).toHaveAttribute('aria-busy', 'true');
  // Meanwhile everything else works: navigation, data, dialogs.
  await nav(page, '套餐与订阅', /\/plans$/);
  await page.getByRole('button', { name: '购买 轻量计划', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Escape');
  await nav(page, '订单记录', /\/orders$/);
  await expect(page.getByRole('heading', { name: '订单记录' })).toBeVisible();
  await nav(page, '总览', /\/dashboard$/);
  await expect(page.getByRole('heading', { level: 1, name: /欢迎回来/ })).toBeVisible();
  const fallback = page.getByTestId('support-fallback');
  await expect(fallback).toBeVisible({ timeout: 25000 });
  const after = Date.now() - clicked;
  expect(after).toBeGreaterThanOrEqual(19000);
  await expect(fallback).toContainText('客服连接较慢，请稍后再试');
  await expect(fallback).toContainText('support@example.test');
  await shot(page, 'support-fallback');
  // Retrying a request that is still pending waits again without a second copy; the late answer
  // is accepted and opens the chat.
  await fallback.getByRole('button', { name: '重试' }).click();
  await expect(page.getByTestId('support-connecting')).toBeVisible();
  expect(fake.scripts()).toHaveLength(1);
  fake.state.mode = 'ok';
  await fake.release();
  await expect.poll(() => calls(page)).toContain('do chat:open');
  await expect(launcher(page)).toHaveCount(0);
  expect(await scriptElements(page)).toBe(1);
  expect(errors).toEqual([]);
  expect(warnings.filter((w) => /support|crisp/i.test(w)).length).toBeLessThanOrEqual(1);
});

test('Crisp fails: the fallback appears at once, retry requests the script again', async ({
  page,
  context,
}) => {
  const fake = await crisp(context, 'fail');
  const { errors } = watch(page);
  await login(page, fixture.emails.supporta);
  await launcher(page).click();
  const fallback = page.getByTestId('support-fallback');
  await expect(fallback).toBeVisible({ timeout: 5000 });
  expect(fake.scripts()).toHaveLength(1);
  // Closing the card leaves the launcher; it asks again on the next click.
  await fallback.getByRole('button', { name: '关闭提示' }).click();
  await expect(fallback).toHaveCount(0);
  await launcher(page).click();
  await expect(fallback).toBeVisible({ timeout: 5000 });
  expect(fake.scripts()).toHaveLength(2);
  fake.state.mode = 'ok';
  await fallback.getByRole('button', { name: '重试' }).click();
  await expect.poll(() => calls(page)).toContain('do chat:open');
  expect(fake.scripts()).toHaveLength(3);
  expect(await scriptElements(page)).toBe(1);
  expect(errors).toEqual([]);
});

test('save-data: nothing is preloaded; a click loads the chat', async ({ page, context }) => {
  const fake = await crisp(context, 'ok');
  await context.addInitScript(() => {
    Object.defineProperty(navigator, 'connection', {
      value: { saveData: true },
      configurable: true,
    });
  });
  // On a phone (390 wide, dark): the launcher leaves the last line of every page free.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ colorScheme: 'dark' });
  await login(page, fixture.emails.supportb);
  await expect(launcher(page)).toBeVisible();
  await expect(page.getByRole('heading', { level: 1, name: /欢迎回来/ })).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  await page.waitForTimeout(300);
  await shot(page, 'support-launcher-mobile-dark');
  const clear = await page.evaluate(() => {
    const button = document
      .querySelector('[data-testid="support-button"]')!
      .getBoundingClientRect();
    const spacer = document.querySelector('.support-spacer')!.getBoundingClientRect();
    // At the bottom of the page only the spacer lies under the launcher.
    return spacer.height >= 72 && spacer.top <= button.top && spacer.bottom >= button.bottom - 1;
  });
  expect(clear).toBe(true);
  await page.waitForTimeout(7000);
  expect(fake.state.requests).toEqual([]);
  await launcher(page).click();
  await expect.poll(() => fake.scripts().length, { timeout: 2000 }).toBe(1);
  await expect.poll(() => calls(page)).toContain('do chat:open');
});

test('administrators and impersonation never load the chat', async ({ page, context }) => {
  test.setTimeout(120_000);
  const fake = await crisp(context, 'ok');
  await login(page, fixture.emails.supportroot);
  await expect(page.getByRole('heading', { level: 1, name: /欢迎回来/ })).toBeVisible();
  await expect(launcher(page)).toHaveCount(0);
  await page.waitForTimeout(6500);
  expect(fake.state.requests).toEqual([]);
  // Impersonating an ordinary user: still nothing.
  const status = await page.evaluate(
    async ({ email, password }) => {
      const list = await (
        await fetch('/api/v1/admin/console/users?q=' + encodeURIComponent(email))
      ).json();
      const response = await fetch(`/api/v1/admin/console/users/${list.users[0].id}/impersonate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ admin_password: password }),
      });
      return response.status;
    },
    { email: fixture.emails.supporta, password: fixture.password },
  );
  expect(status).toBe(200);
  await page.goto('/dashboard');
  await expect(page.getByTestId('impersonation-banner')).toBeVisible();
  await expect(launcher(page)).toHaveCount(0);
  await page.waitForTimeout(6500);
  expect(fake.state.requests).toEqual([]);
  await page
    .getByTestId('impersonation-banner')
    .getByRole('button', { name: '返回管理员' })
    .click();
  await expect(page).toHaveURL(/\/admin\/console\/users$/);
});

test('switched off, or an invalid website ID: no launcher, no request, the API still works', async ({
  page,
  context,
}) => {
  const fake = await crisp(context, 'ok');
  // The server-side parsing is covered by scripts/api-proxy.test.mjs and scripts/support.test.mjs;
  // here the browser gets the answers it would produce, and one malformed by hand.
  for (const value of [null, 'not-a-website-id']) {
    await context.unroute('**/api/runtime-config').catch(() => undefined);
    await context.route('**/api/runtime-config', async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.support.crispWebsiteId = value;
      await route.fulfill({ response, json: body });
    });
    await login(page, fixture.emails.supporta);
    await expect(page.getByRole('heading', { level: 1, name: /欢迎回来/ })).toBeVisible();
    await expect(launcher(page)).toHaveCount(0);
    await expect(page.getByTestId('support-nav')).toHaveCount(0);
    await page.waitForTimeout(6000);
    expect(fake.state.requests).toEqual([]);
    await page.getByRole('button', { name: '账户菜单' }).click();
    await page.getByRole('menuitem', { name: '退出登录' }).click();
    await expect(page).toHaveURL(/\/login$/);
  }
});
