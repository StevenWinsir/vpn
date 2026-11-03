import { expect, test, type Browser, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import { bannedIn } from './copy-words';

// Browser acceptance for mid-period plan upgrades (handoff_plans.md round 2, R2-01 … R2-10). It
// runs against the real API on a private PostgreSQL with the local gateway fake
// (scripts/test-payment-web.py). Every subscription is placed by the fixture when the test needs
// it (upgrade-seed: started `elapsed` seconds ago, ends in `remaining`, paid by one order covering
// the period), so the prices are the exact figures of handoff_plans.md 6 whatever the shared
// clock reads. The seeded starter/pro/max plans are never edited; the upgrade settings and the
// line group this spec adds are put back whatever happens.
type Fixture = {
  api: string;
  gateway: string;
  password: string;
  control: string;
  emails: Record<'upgroot' | 'upga' | 'upgb' | 'upgc' | 'upgd' | 'upge' | 'upgf' | 'upgg', string>;
};
type FixtureOrder = {
  id: string;
  trade_no: string;
  status: string;
  amount_cents: number;
  kind: string;
  upgrade_traffic_bytes: number;
};
type Quote = {
  from_plan_name: string;
  plan_name: string;
  billing_cycle: 'monthly' | 'yearly';
  expires_at: string;
  upgrade_days: number;
  credited_days: number;
  value_cents: number;
  credit_cents: number;
  amount_cents: number;
  traffic_add_bytes: number;
  new_cycle_bytes: number;
  device_limit: number;
  allow_dedicated: boolean;
  lost_groups: string[];
  purchasable: boolean;
};

const GiB = 1024 ** 3;
const DAY = 86400;
const HOUR = 3600;
const cents = (c: number) => `¥${(c / 100).toFixed(2)}`;
const gb = (bytes: number) =>
  `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(bytes / GiB)} GB`;
const lostGroup = 'E2E 轻量专属线路';

async function shot(page: Page, name: string) {
  const dir = `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${name}.png`, animations: 'disabled', fullPage: true });
}

let fixture: Fixture;
test.beforeAll(async () => {
  const filename = process.env.PAYMENT_FIXTURE;
  if (!filename?.startsWith('/tmp/vpn-payment-'))
    throw new Error('Private payment fixture required');
  fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
});

async function control(path: string, method = 'GET') {
  const response = await fetch(fixture.api + '/fixture/' + path, {
    method,
    headers: { 'X-Acceptance-Control': fixture.control },
  });
  if (!response.ok && response.status !== 204)
    throw new Error(`fixture ${path}: ${response.status}`);
  return response.status === 204 ? null : response.json();
}
const q = encodeURIComponent;
const seed = (
  email: string,
  plan: string,
  elapsed: number,
  remaining: number,
  extra: { cycle?: 'yearly'; test?: boolean } = {},
) =>
  control(
    `upgrade-seed?email=${q(email)}&plan=${plan}&elapsed=${elapsed}&remaining=${remaining}` +
      (extra.cycle ? `&cycle=${extra.cycle}` : '') +
      (extra.test ? '&test=1' : ''),
  );
const ordersOf = async (email: string) =>
  (await control('orders?email=' + q(email))) as FixtureOrder[];
const subscriptionOf = async (email: string) =>
  (await control('subscription?email=' + q(email))) as {
    plan_id: string;
    starts_at: string;
    expires_at: string;
    traffic_limit_bytes: number;
    max_devices: number;
    allow_dedicated: boolean;
  };
const pay = (tradeNo: string) => control(`pay?trade_no=${tradeNo}`, 'POST');
const advance = (seconds: number) => control('advance?seconds=' + seconds);

async function login(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

const card = (page: Page, id: string) =>
  page.locator(`[data-testid="plan-card"][data-plan="${id}"]`);
const upgradeButton = (page: Page, name: string) =>
  page.getByRole('button', { name: `升级到 ${name}`, exact: true });
const dialog = (page: Page) => page.getByRole('dialog');
const goPay = (page: Page) => dialog(page).getByRole('button', { name: '去支付', exact: true });
const choose = (page: Page, name: '微信支付' | '支付宝') =>
  dialog(page).getByRole('radio', { name }).click();

// The cards are ready once the upgrade options answered (or the page decided not to ask).
async function openPlans(page: Page, path = '/plans') {
  const options = page.waitForResponse((r) => r.url().includes('/payments/upgrade-options'));
  await page.goto(path);
  expect((await options).status()).toBe(200);
  await expect(page.getByTestId('plan-card')).toHaveCount(3);
}

// Opens the upgrade dialog and returns the quote it shows.
async function openUpgrade(page: Page, name: string): Promise<Quote> {
  const response = page.waitForResponse(
    (r) => r.url().includes('/payments/upgrade-quote') && r.request().method() === 'POST',
  );
  await upgradeButton(page, name).click();
  await expect(dialog(page)).toBeVisible();
  const body = await (await response).json();
  await expect(dialog(page).getByLabel('应付金额', { exact: true })).toBeVisible();
  return body.quote as Quote;
}

async function visitor(browser: Browser, email: string) {
  const context = await browser.newContext();
  const page = await context.newPage();
  await login(page, email);
  return { context, page };
}

// Administrator calls with the signed-in console page's cookies.
async function call(page: Page, path: string, method = 'GET', body?: unknown) {
  return page.evaluate(
    async ([p, m, b]) => {
      const response = await fetch('/api/v1' + p, {
        method: m as string,
        headers: b ? { 'Content-Type': 'application/json' } : undefined,
        body: b ? (b as string) : undefined,
      });
      return { status: response.status, body: await response.json().catch(() => null) };
    },
    [path, method, body === undefined ? '' : JSON.stringify(body)] as const,
  );
}
type Settings = {
  yearly_discount_percent: number;
  upgrade_enabled: boolean;
  upgrade_min_charge_cents: number;
  version: number;
};
async function settingsOf(page: Page) {
  return ((await call(page, '/admin/console/shop-settings')).body as { settings: Settings })
    .settings;
}
// Back to the defaults (on, 1.00) whatever a test left.
async function restoreSettings(page: Page) {
  const s = await settingsOf(page);
  if (s.upgrade_enabled && s.upgrade_min_charge_cents === 100) return;
  const r = await call(page, '/admin/console/shop-settings', 'POST', {
    yearly_discount_percent: s.yearly_discount_percent,
    upgrade_enabled: true,
    upgrade_min_charge_cents: 100,
    version: s.version,
  });
  expect(r.status, JSON.stringify(r.body)).toBe(200);
}

test.describe.configure({ mode: 'serial' });

let admin: Page;
let adminContext: Awaited<ReturnType<Browser['newContext']>>;
test.beforeAll(async ({ browser }) => {
  ({ context: adminContext, page: admin } = await visitor(browser, fixture.emails.upgroot));
  await restoreSettings(admin);
  // upga: example 1 of handoff_plans.md 6.6 (bought 10 d 10 h ago, 19 d 14 h left).
  await seed(fixture.emails.upga, 'starter', 10 * DAY + 10 * HOUR, 19 * DAY + 14 * HOUR);
  // upgb: yearly starter, 10 days in (the first 30-day cycle has 20 days left).
  await seed(fixture.emails.upgb, 'starter', 10 * DAY, 355 * DAY, { cycle: 'yearly' });
  // upgc: pro, 5 days in.
  await seed(fixture.emails.upgc, 'pro', 5 * DAY, 25 * DAY);
  // upgd: a test subscription, which any paid purchase replaces.
  await seed(fixture.emails.upgd, 'starter', DAY, 29 * DAY, { test: true });
  // upge: 20 hours left.
  await seed(fixture.emails.upge, 'starter', 30 * DAY - 20 * HOUR, 20 * HOUR);
});
test.afterAll(async () => {
  try {
    await control(`node-group?action=delete&name=${q(lostGroup)}`);
    await restoreSettings(admin);
  } finally {
    await adminContext?.close();
  }
});

test('R2-01 every card follows its relation to the current plan', async ({ browser }) => {
  // upga holds starter: renew it, upgrade to the two plans above it.
  const a = await visitor(browser, fixture.emails.upga);
  await openPlans(a.page);
  await expect(
    card(a.page, 'starter').getByRole('button', { name: '续费 轻量计划', exact: true }),
  ).toBeEnabled();
  await expect(upgradeButton(a.page, '进阶计划')).toBeEnabled();
  await expect(upgradeButton(a.page, '旗舰计划')).toBeEnabled();
  // The estimates on the cards are the server's (example 1; starter -> max likewise).
  await expect(card(a.page, 'pro').getByTestId('upgrade-hint')).toContainText(
    '补差价约 ¥14.00，到期日不变',
  );
  await expect(card(a.page, 'max').getByTestId('upgrade-hint')).toContainText(
    '补差价约 ¥40.67，到期日不变',
  );
  await expect(a.page.getByTestId('change-rules')).toContainText('有效期内可以升级到更高的套餐');
  await expect(a.page.getByTestId('change-rules')).toContainText('降级不退还差价');
  await shot(a.page, 'upgrade-cards');
  await a.context.close();

  // upgc holds pro: starter is a downgrade that waits for the expiry; max is an upgrade.
  const c = await visitor(browser, fixture.emails.upgc);
  await openPlans(c.page);
  const starter = card(c.page, 'starter');
  await expect(starter.getByRole('button', { name: '购买 轻量计划', exact: true })).toBeDisabled();
  await expect(starter.getByRole('button', { name: '购买 轻量计划' })).toHaveText('到期后可购买');
  const expiry = (await subscriptionOf(fixture.emails.upgc)).expires_at;
  const day = new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai' }).format(
    new Date(expiry),
  );
  await expect(starter.getByTestId('closed-hint')).toHaveText(
    `当前套餐有效期内不支持降级（降级不退还差价），请在 ${day} 到期后购买`,
  );
  await expect(upgradeButton(c.page, '旗舰计划')).toBeEnabled();
  await expect(
    card(c.page, 'pro').getByRole('button', { name: '续费 进阶计划', exact: true }),
  ).toBeEnabled();
  await shot(c.page, 'upgrade-cards-downgrade');
  await c.context.close();

  // upgd holds a test subscription: everything is a purchase.
  const d = await visitor(browser, fixture.emails.upgd);
  await openPlans(d.page);
  for (const name of ['轻量计划', '进阶计划', '旗舰计划'])
    await expect(d.page.getByRole('button', { name: `购买 ${name}`, exact: true })).toBeEnabled();
  await expect(d.page.getByTestId('upgrade-button')).toHaveCount(0);
  await d.context.close();

  // upge has 20 hours left: the upgrade is offered but cannot be bought.
  const e = await visitor(browser, fixture.emails.upge);
  await openPlans(e.page);
  await expect(upgradeButton(e.page, '进阶计划')).toBeDisabled();
  await expect(card(e.page, 'pro').getByTestId('upgrade-hint')).toContainText(
    '剩余不足 1 天，请到期后直接购买新套餐',
  );
  await e.context.close();
});

test('R2-02 R2-03 the upgrade dialog shows the quote; paying upgrades with the expiry kept', async ({
  page,
}) => {
  // A line only starter can use: the quote names it as lost.
  await control(`node-group?name=${q(lostGroup)}&plans=starter`);
  const before = await subscriptionOf(fixture.emails.upga);
  await login(page, fixture.emails.upga);
  await openPlans(page);
  const quote = await openUpgrade(page, '进阶计划');
  const box = dialog(page);
  // Example 1: 20 days charged, 19 credited, V 26.60, K 12.60, 14.00 to pay, about 133.33 GiB more.
  expect(quote).toMatchObject({
    from_plan_name: '轻量计划',
    plan_name: '进阶计划',
    billing_cycle: 'monthly',
    upgrade_days: 20,
    credited_days: 19,
    value_cents: 2660,
    credit_cents: 1260,
    amount_cents: 1400,
    allow_dedicated: true,
    purchasable: true,
  });
  expect(quote.traffic_add_bytes).toBe(214748364800 - 71582788267);
  expect(Date.parse(quote.expires_at)).toBe(Date.parse(before.expires_at));
  await expect(box.getByText('升级到 进阶计划')).toBeVisible();
  await expect(box.getByLabel('套餐变更', { exact: true })).toHaveText('轻量计划 → 进阶计划');
  await expect(box.getByLabel('剩余天数', { exact: true })).toHaveText('收费 20 天，抵扣 19 天');
  await expect(box.getByLabel('新套餐剩余价值', { exact: true })).toHaveText(
    cents(quote.value_cents),
  );
  await expect(box.getByLabel('已付金额抵扣', { exact: true })).toHaveText(
    `-${cents(quote.credit_cents)}`,
  );
  await expect(box.getByLabel('应付金额', { exact: true })).toHaveText('¥14.00');
  await expect(box.getByLabel('新增流量', { exact: true })).toHaveText(
    `新增 ${gb(quote.traffic_add_bytes)} 流量，剩余流量保留`,
  );
  await expect(box.getByLabel('新增流量', { exact: true })).toContainText('133.33 GB');
  await expect(box.getByLabel('设备数', { exact: true })).toHaveText(
    `最多 ${quote.device_limit} 台设备同时在线`,
  );
  await expect(box.getByLabel('线路权限', { exact: true })).toHaveText('普通直连 + 专线权限');
  await expect(box.getByTestId('reauth-notice')).toContainText('需要重新登录');
  await expect(box.getByTestId('lost-groups')).toContainText(lostGroup);
  expect(quote.lost_groups).toEqual([lostGroup]);
  // No coupon on an upgrade.
  await expect(box.getByLabel('优惠码（可留空）', { exact: true })).toHaveCount(0);
  await expect(box.getByTestId('min-charge')).toHaveCount(0);
  await expect(goPay(page)).toBeDisabled();
  expect(bannedIn(await box.innerText())).toEqual([]);
  await shot(page, 'upgrade-dialog');
  await control(`node-group?action=delete&name=${q(lostGroup)}`);

  // Pay through the gateway fake.
  await choose(page, '支付宝');
  await goPay(page).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  const order = (await ordersOf(fixture.emails.upga)).at(-1)!;
  expect(order).toMatchObject({ kind: 'upgrade', status: 'pending', amount_cents: 1400 });
  await pay(order.trade_no);
  await page.waitForURL(/\/pay\/result\?order=/, { timeout: 30000 });
  await expect(page.getByRole('heading', { name: '已升级到 进阶计划', exact: true })).toBeVisible();
  await expect(page.getByTestId('pay-result')).toContainText('（不变）');
  await expect(page.getByTestId('pay-result')).toContainText('请重新登录');
  await expect(page.getByTestId('pay-result')).toContainText('轻量计划 → 进阶计划');
  await shot(page, 'upgrade-result');

  // The subscription: new plan and entitlements, same period, more traffic.
  const after = await subscriptionOf(fixture.emails.upga);
  const paid = (await ordersOf(fixture.emails.upga)).at(-1)!;
  expect(paid.status).toBe('paid');
  expect(after.plan_id).toBe('pro');
  expect(after.starts_at).toBe(before.starts_at);
  expect(after.expires_at).toBe(before.expires_at);
  expect(after.allow_dedicated).toBe(true);
  expect(after.max_devices).toBe(5);
  expect(paid.upgrade_traffic_bytes).toBeGreaterThan(0);
  expect(paid.upgrade_traffic_bytes).toBeLessThanOrEqual(quote.traffic_add_bytes);
  expect(after.traffic_limit_bytes).toBe(before.traffic_limit_bytes + paid.upgrade_traffic_bytes);

  // The order page and the overview agree.
  await page.getByRole('link', { name: '订单记录', exact: true }).first().click();
  const row = page.getByRole('row').filter({ hasText: paid.id.slice(0, 8) });
  await expect(row.getByTestId('order-upgrade')).toHaveText('升级 轻量计划 → 进阶计划');
  await expect(row.getByText('补差价', { exact: true })).toBeVisible();
  await expect(row).toContainText(`抵扣 ¥12.60 · 20 天 · +${gb(paid.upgrade_traffic_bytes)}`);
  await expect(row.getByText('¥14.00')).toBeVisible();
  await expect(row.getByText('已支付', { exact: true })).toBeVisible();
  await shot(page, 'upgrade-orders');
  await page.goto('/dashboard');
  await expect(page.getByRole('heading', { name: '进阶计划', exact: true })).toBeVisible();
  // Now on pro: starter is a downgrade, max the next upgrade.
  await openPlans(page);
  await expect(page.getByRole('button', { name: '购买 轻量计划', exact: true })).toBeDisabled();
  await expect(upgradeButton(page, '旗舰计划')).toBeEnabled();
});

test('R2-02 a yearly holding shows the cycle allowance it moves to', async ({ page }) => {
  await login(page, fixture.emails.upgb);
  await openPlans(page);
  const quote = await openUpgrade(page, '进阶计划');
  // Just under 355 days left: 355 charged and 354 credited, both at the yearly prices
  // (430.92 x 355 / 365 up, 214.92 x 354 / 365 down).
  expect(quote).toMatchObject({
    billing_cycle: 'yearly',
    upgrade_days: 355,
    credited_days: 354,
    value_cents: 41912,
    credit_cents: 20844,
    amount_cents: 21068,
    new_cycle_bytes: 300 * GiB,
  });
  const box = dialog(page);
  await expect(box.getByLabel('应付金额', { exact: true })).toHaveText('¥210.68');
  await expect(box.getByLabel('本周期新增流量', { exact: true })).toHaveText(
    `本周期新增 ${gb(quote.traffic_add_bytes)}，剩余流量保留`,
  );
  await expect(box.getByLabel('本周期新增流量', { exact: true })).toContainText('133.33 GB');
  await expect(box.getByLabel('之后每周期流量', { exact: true })).toHaveText(
    '之后每 30 天重置为 300 GB',
  );
});

test('R2-04 a price that moved across a day boundary is shown before the gateway opens', async ({
  page,
}) => {
  // 10 days and 90 seconds left: 11 days charged, 10 credited.
  await seed(fixture.emails.upgf, 'starter', 20 * DAY - 90, 10 * DAY + 90);
  await login(page, fixture.emails.upgf);
  await openPlans(page);
  const first = await openUpgrade(page, '进阶计划');
  expect(first).toMatchObject({ upgrade_days: 11, credited_days: 10, amount_cents: 800 });
  await expect(dialog(page).getByLabel('应付金额', { exact: true })).toHaveText('¥8.00');
  await choose(page, '微信支付');
  // Two minutes later the remaining time is under 10 days: 10 charged, 9 credited.
  await advance(120);
  const requote = page.waitForResponse((r) => r.url().includes('/payments/upgrade-quote'));
  await goPay(page).click();
  await requote;
  await expect(dialog(page).getByTestId('price-updated')).toHaveText('价格已更新，请确认后再支付');
  await expect(dialog(page).getByLabel('应付金额', { exact: true })).toHaveText('¥7.33');
  await expect(dialog(page).getByLabel('剩余天数', { exact: true })).toHaveText(
    '收费 10 天，抵扣 9 天',
  );
  await expect(page).toHaveURL(/\/plans/);
  // Nothing was ordered: only the seeded purchase exists.
  expect((await ordersOf(fixture.emails.upgf)).length).toBe(1);
  await shot(page, 'upgrade-price-changed');
});

test('R2-05 an unfinished order holds the upgrade back and points to the orders', async ({
  page,
}) => {
  await seed(fixture.emails.upgg, 'starter', 10 * DAY, 20 * DAY);
  await control('pending-order?email=' + q(fixture.emails.upgg));
  await login(page, fixture.emails.upgg);
  await openPlans(page);
  await openUpgrade(page, '进阶计划');
  const box = dialog(page);
  await expect(box.getByTestId('upgrade-pending')).toContainText('你有未完成的订单');
  await choose(page, '支付宝');
  await goPay(page).click();
  await expect(box.getByTestId('upgrade-pending')).toContainText('等待约 2 分钟后再试');
  await expect(page).toHaveURL(/\/plans/);
  expect((await ordersOf(fixture.emails.upgg)).filter((o) => o.kind === 'upgrade')).toEqual([]);
  await box.getByRole('link', { name: '查看订单记录' }).click();
  await expect(page).toHaveURL(/\/orders$/);
});

test('R2-06 the yearly view keeps the upgrade button and price, and says why', async ({ page }) => {
  await login(page, fixture.emails.upgc);
  await openPlans(page);
  const monthly = await card(page, 'max').getByTestId('upgrade-hint').innerText();
  expect(monthly).toContain('补差价约 ¥34.67');
  await page.getByRole('radio', { name: /^年付/ }).click();
  await expect(page).toHaveURL(/cycle=yearly/);
  const hint = card(page, 'max').getByTestId('upgrade-hint');
  await expect(hint).toContainText('补差价约 ¥34.67，到期日不变');
  await expect(hint).toContainText('如需年付，可在升级后续费为年付');
  await openUpgrade(page, '旗舰计划');
  await expect(dialog(page).getByLabel('应付金额', { exact: true })).toHaveText('¥34.67');
  await expect(dialog(page)).toContainText('升级按你当前的付费方式补差价');
});

test('R2-07 an older backend without upgrades: no upgrade request, wait for the expiry', async ({
  page,
}) => {
  await page.route('**/api/v1/meta', async (route) => {
    const response = await route.fetch();
    const meta = await response.json();
    delete meta.plan_upgrade_supported;
    await route.fulfill({ response, json: meta });
  });
  const asked: string[] = [];
  page.on('request', (r) => {
    if (r.url().includes('/payments/upgrade-')) asked.push(r.url());
  });
  await login(page, fixture.emails.upgc);
  await page.goto('/plans');
  await expect(page.getByTestId('plan-card')).toHaveCount(3);
  for (const id of ['starter', 'max']) {
    await expect(card(page, id).getByRole('button', { name: /^购买 / })).toBeDisabled();
    await expect(card(page, id).getByRole('button', { name: /^购买 / })).toHaveText('到期后可购买');
    await expect(card(page, id).getByTestId('closed-hint')).toHaveText(
      '当前套餐有效期内不能更换，到期后可购买。',
    );
  }
  await expect(page.getByTestId('upgrade-button')).toHaveCount(0);
  await expect(page.getByTestId('change-rules')).toContainText('有效期内不能更换为其他套餐');
  await expect(page.getByTestId('change-rules')).not.toContainText('升级');
  expect(asked).toEqual([]);
});

test('R2-08 the console shows the upgrade, and its settings switch and price the upgrades', async ({
  browser,
}) => {
  // The paid upgrade of R2-03 with its computation.
  await admin.goto(`/admin/console/orders?kind=upgrade&email=${q(fixture.emails.upga)}`);
  await expect(admin.getByRole('textbox', { name: '订单类型' })).toHaveValue('升级');
  const row = admin.getByTestId('order-row').first();
  await expect(row.getByTestId('order-kind')).toContainText('升级');
  await expect(row.getByTestId('order-kind')).toContainText('从 轻量计划');
  await row.getByRole('button', { name: /^查看订单/ }).click();
  const detail = admin.getByTestId('upgrade-detail');
  await expect(detail).toContainText('收费 20 天（Nc），抵扣 19 天（Nk）');
  await expect(detail).toContainText('¥26.60');
  await expect(detail).toContainText('¥12.60（K）');
  await expect(detail).toContainText('月付');
  await expect(admin.getByTestId('order-detail')).toContainText('升级：轻量计划 → 进阶计划');
  await expect(admin.getByTestId('order-detail')).toContainText('价值覆盖');
  await shot(admin, 'console-upgrade-detail');

  // The minimum charge: validated, saved and audited.
  await admin.goto('/admin/console/plans');
  const bar = admin.getByTestId('upgrade-bar');
  await expect(bar.getByTestId('upgrade-current')).toHaveText(
    '有效期内升级：已开启，最低补差 ¥1.00',
  );
  const minimum = bar.getByLabel('最低补差金额（元）', { exact: true });
  await minimum.fill('0');
  await bar.getByRole('button', { name: '保存升级设置' }).click();
  await expect(bar).toContainText('最低补差金额须为 0.01–100.00 元');
  await minimum.fill('2.00');
  await bar.getByRole('button', { name: '保存升级设置' }).click();
  await expect(admin.getByTestId('upgrade-current')).toHaveText(
    '有效期内升级：已开启，最低补差 ¥2.00',
  );
  const audit = (await control('audit?action=admin.shop_settings')) as {
    detail: Record<string, { from: unknown; to: unknown }>;
  };
  expect(audit.detail.upgrade_min_charge_cents).toEqual({ from: 100, to: 200 });

  // Switched off: the cards say so and an open dialog's order is refused plainly.
  const c = await visitor(browser, fixture.emails.upgc);
  await openPlans(c.page);
  await openUpgrade(c.page, '旗舰计划');
  await choose(c.page, '支付宝');
  await admin.getByTestId('upgrade-bar').getByRole('switch', { name: '允许有效期内升级' }).click({
    force: true,
  });
  await admin.getByTestId('upgrade-bar').getByRole('button', { name: '保存升级设置' }).click();
  await expect(admin.getByTestId('upgrade-current')).toHaveText(
    '有效期内升级：已关闭，最低补差 ¥2.00',
  );
  const off = (await control('audit?action=admin.shop_settings')) as {
    detail: Record<string, { from: unknown; to: unknown }>;
  };
  expect(off.detail.upgrade_enabled).toEqual({ from: true, to: false });
  await goPay(c.page).click();
  await expect(
    dialog(c.page).getByRole('alert').filter({ hasText: '暂未开放有效期内升级' }).first(),
  ).toBeVisible();
  await expect(goPay(c.page)).toBeDisabled();
  await expect(c.page).toHaveURL(/\/plans/);
  await c.page.keyboard.press('Escape');
  await openPlans(c.page);
  await expect(upgradeButton(c.page, '旗舰计划')).toBeDisabled();
  await expect(card(c.page, 'max').getByTestId('upgrade-hint')).toContainText(
    '暂未开放有效期内升级',
  );
  await shot(c.page, 'upgrade-disabled');
  await c.context.close();
  await restoreSettings(admin);
});

test('R2-09 copy, names and the dark theme', async ({ browser }) => {
  const context = await browser.newContext({ colorScheme: 'dark' });
  const page = await context.newPage();
  await login(page, fixture.emails.upgc);
  await openPlans(page);
  await expect(page.locator('html')).toHaveAttribute('data-mantine-color-scheme', 'dark');
  expect(bannedIn(await page.locator('main').innerText())).toEqual([]);
  await expect(upgradeButton(page, '旗舰计划')).toHaveAccessibleName('升级到 旗舰计划');
  await openUpgrade(page, '旗舰计划');
  expect(bannedIn(await dialog(page).innerText())).toEqual([]);
  await shot(page, 'upgrade-dialog-dark');
  await context.close();
});
