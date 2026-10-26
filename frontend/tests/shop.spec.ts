import { expect, test, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';

// Browser acceptance for the plan shop (handoff_shop.md round 3, S3-01 … S3-09): monthly/yearly
// switch, yearly cards, checkout through the local fake gateway, renewal, price change while
// the dialog is open, plans without a yearly tier and the test-purchase dialog. It runs against
// the real API on a private PostgreSQL (scripts/test-payment-web.py).
//
// The seeded starter/pro/max plans and the global discount are never changed: this spec creates
// its own e2e-shop-* plans through the administrator API and takes them off sale at the end.
type Fixture = {
  api: string;
  gateway: string;
  password: string;
  control: string;
  emails: Record<'shopa' | 'shopb' | 'shopc' | 'shopd' | 'root', string>;
};
type FixtureOrder = { id: string; trade_no: string; status: string; amount_cents: number };
type Tier = {
  price_cents: number;
  original_cents: number;
  saving_cents: number;
  discount_percent: number;
  duration_days: number;
  traffic_bytes: number;
  monthly_equivalent_cents: number;
};
type PublicPlan = {
  id: string;
  name: string;
  price_cents: number;
  traffic_bytes: number;
  max_devices: number;
  featured: boolean;
  monthly: Tier;
  yearly: Tier | null;
};

const GiB = 1024 ** 3;
const cents = (c: number) => `¥${(c / 100).toFixed(2)}`;
const gibOf = (bytes: number) =>
  `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(bytes / GiB)} GiB`;

async function shot(page: Page, name: string) {
  const dir = `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${name}.png`, fullPage: true, animations: 'disabled' });
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
const ordersOf = async (email: string) =>
  (await control('orders?email=' + encodeURIComponent(email))) as FixtureOrder[];
const lastOrder = async (email: string) => (await ordersOf(email)).at(-1)!;
const subscriptionOf = async (email: string) =>
  (await control('subscription?email=' + encodeURIComponent(email))) as {
    plan_id: string;
    expires_at: string;
    traffic_limit_bytes: number;
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

// Administrator calls go through the signed-in page itself, so the cookies and the refresh
// behave as in the real console.
let adminPage: Page;
type Call = {
  status: number;
  json: { plans: { id: string; version: number; [key: string]: unknown }[] };
};
async function admin(path: string, method = 'GET', body?: unknown): Promise<Call> {
  return adminPage.evaluate(
    async ({ path, method, body }) => {
      const go = () =>
        fetch('/api/v1' + path, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: body === undefined ? undefined : JSON.stringify(body),
        });
      let response = await go();
      if (response.status === 401) {
        await fetch('/api/v1/auth/refresh', { method: 'POST' });
        response = await go();
      }
      return { status: response.status, json: await response.json().catch(() => null) };
    },
    { path, method, body },
  );
}
const planBody = (over: Record<string, unknown>) => ({
  name: 'E2E',
  description: '',
  note: '',
  price_cents: 2500,
  duration_days: 30,
  traffic_bytes: 10 * GiB,
  max_devices: 2,
  allow_dedicated: false,
  yearly_enabled: true,
  yearly_duration_days: 365,
  yearly_traffic_bytes: null,
  yearly_discount_percent: null,
  featured: false,
  sort: 100,
  active: true,
  ...over,
});
async function setPlan(id: string, patch: Record<string, unknown>) {
  const list = await admin('/admin/console/plans');
  const current = list.json.plans.find((p) => p.id === id)!;
  const keys = Object.keys(planBody({}));
  const next = Object.fromEntries(keys.map((k) => [k, current[k]]));
  const response = await admin(`/admin/console/plans/${id}`, 'POST', {
    ...next,
    ...patch,
    version: current.version,
  });
  expect(response.status, JSON.stringify(response.json)).toBe(200);
}

const month = 'e2e-shop-month'; // 25.00 / month, follows the global 9 折
const monthName = 'E2E 周期套餐';
const plain = 'e2e-shop-plain'; // monthly only
const price = 'e2e-shop-price'; // its price is changed while a dialog is open
const deep = 'e2e-shop-deep'; // own discount 80
const mine = [month, plain, price, deep];

test.describe.configure({ mode: 'serial' });
test.beforeAll(async ({ browser }) => {
  const context = await browser.newContext();
  adminPage = await context.newPage();
  await login(adminPage, fixture.emails.root);
  const create = async (id: string, over: Record<string, unknown>) => {
    const response = await admin('/admin/console/plans', 'POST', planBody({ id, ...over }));
    expect([200, 201], JSON.stringify(response.json)).toContain(response.status);
  };
  await create(month, { name: monthName, description: '端到端测试套餐', sort: 100 });
  await create(price, { name: 'E2E 调价套餐', price_cents: 3000, sort: 101 });
  await create(plain, {
    name: 'E2E 仅月付',
    price_cents: 1500,
    yearly_enabled: false,
    sort: 102,
    active: false,
  });
  await create(deep, {
    name: 'E2E 深折套餐',
    yearly_discount_percent: 80,
    sort: 103,
    active: false,
  });
});
test.afterAll(async () => {
  for (const id of mine) await setPlan(id, { active: false }).catch(() => undefined);
  await adminPage?.context().close();
});

async function publicPlans(page: Page): Promise<PublicPlan[]> {
  return page.evaluate(async () => (await (await fetch('/api/v1/plans')).json()).plans);
}
const card = (page: Page, id: string) =>
  page.locator(`[data-testid="plan-card"][data-plan="${id}"]`);
const cycleRadio = (page: Page, name: '月付' | '年付') =>
  page.getByRole('radio', { name: new RegExp(`^${name}`) });
const dialog = (page: Page) => page.getByRole('dialog');
const goPay = (page: Page) => page.getByRole('button', { name: '去支付', exact: true });
const choose = (page: Page, name: '微信支付' | '支付宝') =>
  page.getByRole('radio', { name }).click();

test('S3-01 monthly is the default and the cards read as before', async ({ page }) => {
  await page.goto('/plans');
  await expect(cycleRadio(page, '月付')).toHaveAttribute('aria-checked', 'true');
  await expect(cycleRadio(page, '年付')).toHaveAttribute('aria-checked', 'false');
  await expect(page).not.toHaveURL(/cycle=/);
  await expect(card(page, 'pro')).toContainText('¥39.90');
  await expect(card(page, 'pro')).toContainText('/ 30 天');
  await expect(card(page, 'pro')).toContainText(/最多 \d+ 台设备同时在线/);
  await expect(page.getByText('待执行')).toHaveCount(0);
  // The labels the earlier specs rely on are unchanged.
  await expect(page.getByRole('button', { name: '购买 进阶计划', exact: true })).toBeVisible();
  await expect(page.getByLabel('年付价格')).toHaveCount(0);
  // Featured styling comes from the API flag.
  const plans = await publicPlans(page);
  for (const p of plans) {
    if (p.featured) await expect(card(page, p.id)).toHaveClass(/featured/);
    else await expect(card(page, p.id)).not.toHaveClass(/featured/);
  }
});

test('S3-02 yearly cards show the server figures and the state lives in the address', async ({
  page,
}) => {
  await page.goto('/plans');
  await cycleRadio(page, '年付').click();
  await expect(page).toHaveURL(/\/plans\?cycle=yearly$/);
  await expect(cycleRadio(page, '年付')).toHaveAttribute('aria-checked', 'true');
  const plans = (await publicPlans(page)).filter((p) => p.yearly);
  expect(plans.length).toBeGreaterThanOrEqual(5);
  for (const p of plans) {
    const t = p.yearly!;
    const c = card(page, p.id);
    await expect(c.getByLabel('年付价格')).toHaveText(cents(t.price_cents));
    await expect(c.getByLabel('原价')).toHaveText(cents(t.original_cents));
    await expect(c.getByLabel('立省金额')).toHaveText(`立省 ${cents(t.saving_cents)}`);
    await expect(c).toContainText(`折合 ${cents(t.monthly_equivalent_cents)} / 月`);
    await expect(c).toContainText('/ 年');
    await expect(c).toContainText(`${gibOf(t.traffic_bytes)} 计费流量`);
    await expect(c).toContainText(`有效期 ${t.duration_days} 天`);
    await expect(c.getByTestId('plan-ribbon')).toHaveText(
      `年付 ${t.discount_percent === 90 ? '9' : t.discount_percent / 10} 折`,
    );
    await expect(
      c.getByRole('button', { name: `购买 ${p.name}（年付）`, exact: true }),
    ).toBeVisible();
    await expect(c.getByRole('button')).toContainText('登录后购买');
  }
  expect((await publicPlans(page)).find((p) => p.id === 'pro')!.yearly!.price_cents).toBe(43092);
  await expect(card(page, 'pro').getByLabel('年付价格')).toHaveText('¥430.92');
  // Refresh keeps the cycle; back to monthly removes the parameter.
  await page.reload();
  await expect(cycleRadio(page, '年付')).toHaveAttribute('aria-checked', 'true');
  await expect(card(page, 'pro').getByLabel('年付价格')).toHaveText('¥430.92');
  await cycleRadio(page, '月付').click();
  await expect(page).toHaveURL(/\/plans$/);
  await expect(card(page, 'pro')).toContainText('¥39.90');
  // The control is a keyboard-operable radio group.
  await cycleRadio(page, '月付').focus();
  await page.keyboard.press('ArrowRight');
  await expect(cycleRadio(page, '年付')).toHaveAttribute('aria-checked', 'true');
  await expect(page).toHaveURL(/cycle=yearly/);
  await page.keyboard.press('ArrowLeft');
  await expect(cycleRadio(page, '月付')).toHaveAttribute('aria-checked', 'true');
  await expect(page).not.toHaveURL(/cycle=/);
});

test('S3-03 the yearly offer is hard to miss: badge, banner, narrow screen, reduced motion', async ({
  page,
}) => {
  await page.goto('/plans');
  const plans = (await publicPlans(page)).filter((p) => p.yearly);
  const most = Math.max(...plans.map((p) => p.yearly!.saving_cents));
  await expect(page.getByTestId('cycle-badge')).toHaveText('9 折');
  const banner = page.getByTestId('yearly-banner');
  await expect(banner).toHaveText(`切换年付，全部套餐享 9 折，最多立省 ${cents(most)}`);
  await shot(page, 'shop-1440-monthly');
  await banner.click();
  await expect(page).toHaveURL(/cycle=yearly/);
  await expect(banner).toHaveCount(0);
  await expect(page.getByTestId('cycle-badge')).toBeVisible();
  await shot(page, 'shop-1440-yearly');

  // Own discounts: the badge reads 最高省 instead of one 折 value.
  await setPlan(deep, { active: true });
  await page.goto('/plans');
  await expect(page.getByTestId('cycle-badge')).toHaveText('最高省 20%');
  await page.goto('/plans?cycle=yearly');
  await expect(card(page, deep).getByTestId('plan-ribbon')).toHaveText('年付 8 折');
  await expect(card(page, 'pro').getByTestId('plan-ribbon')).toHaveText('年付 9 折');
  await setPlan(deep, { active: false });

  // 375 px: nothing wraps or leaves the screen.
  await page.setViewportSize({ width: 375, height: 800 });
  for (const url of ['/plans', '/plans?cycle=yearly']) {
    await page.goto(url);
    await expect(page.getByTestId('cycle-switch')).toBeVisible();
    const seg = (await page.getByTestId('cycle-switch').boundingBox())!;
    expect(seg.x).toBeGreaterThanOrEqual(0);
    expect(seg.x + seg.width).toBeLessThanOrEqual(375);
    const monthly = (await cycleRadio(page, '月付').boundingBox())!;
    const yearly = (await cycleRadio(page, '年付').boundingBox())!;
    expect(Math.abs(monthly.y - yearly.y)).toBeLessThan(2);
    expect(Math.abs(monthly.height - yearly.height)).toBeLessThan(2);
    expect(yearly.height).toBeLessThan(48);
    const badge = (await page.getByTestId('cycle-badge').boundingBox())!;
    expect(badge.height).toBeLessThan(28);
    expect(badge.x + badge.width).toBeLessThanOrEqual(yearly.x + yearly.width + 1);
    if (url.includes('yearly')) {
      const struck = (await card(page, 'pro').getByLabel('原价').boundingBox())!;
      expect(struck.height).toBeLessThan(24);
      expect(struck.x + struck.width).toBeLessThanOrEqual(375);
      const main = (await card(page, 'pro').getByLabel('年付价格').boundingBox())!;
      expect(main.height).toBeLessThan(60);
    } else {
      const wide = (await page.getByTestId('yearly-banner').boundingBox())!;
      expect(wide.x + wide.width).toBeLessThanOrEqual(375);
    }
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true);
    await shot(page, url.includes('yearly') ? 'shop-375-yearly' : 'shop-375-monthly');
  }

  // The entrance animation runs only when motion is allowed.
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto('/plans');
  await expect(page.getByTestId('cycle-badge')).toHaveCSS('animation-name', 'cycle-badge-in');
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/plans');
  await expect(page.getByTestId('cycle-badge')).toHaveCSS('animation-name', 'none');
});

test('S3-04 yearly payment through the gateway, then the result page, orders and subscription', async ({
  page,
}) => {
  const posted: { url: string; body: Record<string, unknown> }[] = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && /\/payments\/(quote|orders)$/.test(request.url()))
      posted.push({ url: request.url(), body: JSON.parse(request.postData() ?? '{}') });
  });
  await login(page, fixture.emails.shopa);

  // A monthly checkout first: the requests keep the shape an older backend accepts.
  await page.goto('/plans');
  await page.getByRole('button', { name: `购买 ${monthName}`, exact: true }).click();
  await expect(dialog(page).getByLabel('应付金额')).toHaveText('¥25.00');
  const monthlyQuote = posted.at(-1)!.body;
  expect(Object.keys(monthlyQuote).sort()).toEqual(['coupon_code', 'plan_id']);
  await page.keyboard.press('Escape');

  await page.goto('/plans?cycle=yearly');
  await page.getByRole('button', { name: `购买 ${monthName}（年付）`, exact: true }).click();
  await expect(dialog(page)).toBeVisible();
  await expect(dialog(page).getByText(`购买 ${monthName}（年付）`)).toBeVisible();
  await expect(dialog(page).getByText('120 GiB · 365 天')).toBeVisible();
  await expect(dialog(page).getByText('原价（12 个月）')).toBeVisible();
  await expect(dialog(page).getByLabel('原价')).toHaveText('¥300.00');
  await expect(dialog(page).getByLabel('年付优惠')).toHaveText('-¥30.00');
  await expect(dialog(page).getByLabel('应付金额')).toHaveText('¥270.00');
  expect(posted.at(-1)!.body).toMatchObject({ plan_id: month, billing_cycle: 'yearly' });
  await choose(page, '微信支付');
  await shot(page, 'shop-checkout-yearly');
  await goPay(page).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  const order = await lastOrder(fixture.emails.shopa);
  expect(order).toMatchObject({ status: 'pending', amount_cents: 27000 });
  const placed = posted.at(-1)!;
  expect(placed.url).toMatch(/\/payments\/orders$/);
  expect(placed.body).toMatchObject({
    plan_id: month,
    billing_cycle: 'yearly',
    expected_amount_cents: 27000,
  });
  expect(new URL(page.url()).searchParams.get('name')).toBe(`${monthName}·年付`);
  expect(new URL(page.url()).searchParams.get('money')).toBe('270.00');

  await pay(order.trade_no);
  await page.waitForURL(/\/pay\/result\?order=/, { timeout: 30000 });
  await expect(page.getByRole('heading', { name: '已到账', exact: true })).toBeVisible();
  await expect(page.getByTestId('pay-result').getByText('年付', { exact: true })).toBeVisible();
  await page.getByRole('link', { name: '订单记录', exact: true }).first().click();
  const row = page.getByRole('row').filter({ hasText: order.id.slice(0, 8) });
  await expect(row.getByText('年付', { exact: true })).toBeVisible();
  await expect(row.getByText('¥270.00')).toBeVisible();

  const summary = await page.evaluate(async () => {
    const d = await (await fetch('/api/v1/me/dashboard')).json();
    return d.subscription;
  });
  expect((Date.parse(summary.expires_at) - Date.parse(summary.starts_at)) / 86400000).toBe(365);
  expect(summary.traffic_limit_bytes).toBe(120 * GiB);
  expect(summary.plan_id).toBe(month);
  await page.goto('/dashboard');
  await expect(page.getByRole('heading', { name: monthName, exact: true })).toBeVisible();
});

test('S3-05 a monthly subscriber renews as yearly and the expiry moves 365 days', async ({
  page,
}) => {
  await login(page, fixture.emails.shopb);
  await page.goto('/plans');
  await page.getByRole('button', { name: `购买 ${monthName}`, exact: true }).click();
  await choose(page, '支付宝');
  await goPay(page).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  await pay((await lastOrder(fixture.emails.shopb)).trade_no);
  await page.waitForURL(/\/pay\/result\?order=/, { timeout: 30000 });
  await expect(page.getByRole('heading', { name: '已到账', exact: true })).toBeVisible();
  const before = await subscriptionOf(fixture.emails.shopb);
  expect(before.traffic_limit_bytes).toBe(10 * GiB);

  await page.goto('/plans?cycle=yearly');
  const renew = card(page, month).getByRole('button', {
    name: `续费 ${monthName}（年付）`,
    exact: true,
  });
  await expect(renew).toContainText('续费为年付');
  // Other plans stay closed during the validity period.
  await expect(card(page, price).getByRole('button', { name: /^购买 / })).toBeDisabled();
  await renew.click();
  await expect(dialog(page).getByText(`续费 ${monthName}（年付）`)).toBeVisible();
  await expect(dialog(page).getByText(/在当前到期时间基础上顺延 365 天/)).toBeVisible();
  await expect(dialog(page).getByLabel('应付金额')).toHaveText('¥270.00');
  await choose(page, '微信支付');
  await goPay(page).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  await pay((await lastOrder(fixture.emails.shopb)).trade_no);
  await page.waitForURL(/\/pay\/result\?order=/, { timeout: 30000 });
  await expect(page.getByRole('heading', { name: '已到账', exact: true })).toBeVisible();
  const after = await subscriptionOf(fixture.emails.shopb);
  expect((Date.parse(after.expires_at) - Date.parse(before.expires_at)) / 86400000).toBe(365);
  expect(after.traffic_limit_bytes).toBe(130 * GiB);
  // Both cycles are in the order list.
  await page.goto('/orders');
  await expect(page.getByRole('cell', { name: '月付', exact: true })).toHaveCount(1);
  await expect(page.getByRole('cell', { name: '年付', exact: true })).toHaveCount(1);
});

test('S3-06 a price that moves under an open dialog is shown before anything is ordered', async ({
  page,
}) => {
  await login(page, fixture.emails.shopc);
  await page.goto('/plans?cycle=yearly');
  await page.getByRole('button', { name: '购买 E2E 调价套餐（年付）', exact: true }).click();
  await expect(dialog(page).getByLabel('应付金额')).toHaveText('¥324.00');
  await choose(page, '微信支付');
  await setPlan(price, { price_cents: 3500 });
  await goPay(page).click();
  await expect(dialog(page).getByText('价格已更新，请确认后再支付')).toBeVisible();
  await expect(dialog(page).getByLabel('应付金额')).toHaveText('¥378.00');
  await expect(page).toHaveURL(/\/plans\?cycle=yearly$/);
  expect(await ordersOf(fixture.emails.shopc)).toHaveLength(0);
  await shot(page, 'shop-price-updated');
  await goPay(page).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  expect(await lastOrder(fixture.emails.shopc)).toMatchObject({
    status: 'pending',
    amount_cents: 37800,
  });
  await expect(page.locator('#amount')).toHaveText('378.00');
});

test('S3-07 a plan without a yearly tier says so; no yearly tier anywhere hides the control', async ({
  page,
}) => {
  await setPlan(plain, { active: true });
  await page.goto('/plans?cycle=yearly');
  const c = card(page, plain);
  await expect(c.getByTestId('no-yearly')).toHaveText('该套餐暂不提供年付');
  await expect(c.getByLabel('年付价格')).toHaveCount(0);
  await expect(c.getByTestId('plan-ribbon')).toHaveCount(0);
  // The other cards are still yearly.
  await expect(card(page, 'pro').getByLabel('年付价格')).toBeVisible();
  await c.getByRole('button', { name: '查看月付', exact: true }).click();
  await expect(page).toHaveURL(/\/plans$/);
  await expect(c).toContainText('¥15.00');
  await expect(c).toContainText('/ 30 天');
  // With one plan lacking the tier the banner no longer promises "全部套餐".
  await expect(page.getByTestId('yearly-banner')).toContainText('年付套餐享 9 折');
  await setPlan(plain, { active: false });

  // Every plan without a yearly tier: no control, no banner, whatever the address says.
  await page.route('**/api/v1/plans', async (route) => {
    const response = await route.fetch();
    const json = await response.json();
    for (const p of json.plans) p.yearly = null;
    await route.fulfill({ response, json });
  });
  await page.goto('/plans?cycle=yearly');
  await expect(card(page, 'pro')).toContainText('¥39.90');
  await expect(page.getByTestId('cycle-switch')).toHaveCount(0);
  await expect(page.getByTestId('yearly-banner')).toHaveCount(0);
  await expect(page.getByTestId('no-yearly')).toHaveCount(0);
  await expect(page.getByRole('button', { name: '购买 进阶计划', exact: true })).toBeVisible();
});

test('S3-08 buy again after a timeout returns to the yearly plan with the method', async ({
  page,
}) => {
  await login(page, fixture.emails.shopd);
  await page.goto('/plans?cycle=yearly');
  await page.getByRole('button', { name: `购买 ${monthName}（年付）`, exact: true }).click();
  await choose(page, '微信支付');
  await goPay(page).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  await advance(125);
  await expect(page.locator('#state')).toHaveText('timeout');
  await page.getByRole('link', { name: '返回商户页面' }).click();
  await page.waitForURL(/\/pay\/result\?order=/);
  await expect(page.getByRole('heading', { name: '订单已超时，未扣款' })).toBeVisible();
  await page.getByRole('link', { name: '重新购买' }).click();
  await expect(page).toHaveURL(new RegExp(`/plans\\?plan=${month}&cycle=yearly&pay=wxpay$`));
  await expect(cycleRadio(page, '年付')).toHaveAttribute('aria-checked', 'true');
  await expect(dialog(page)).toBeVisible();
  await expect(dialog(page).getByText(`购买 ${monthName}（年付）`)).toBeVisible();
  await expect(page.getByRole('radio', { name: '微信支付' })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await expect(dialog(page).getByLabel('应付金额')).toHaveText('¥270.00');
});

test('S3-09 the test-purchase dialog offers yearly and sends the cycle only for yearly', async ({
  page,
}) => {
  await login(page, fixture.emails.shopd);
  // The fixture runs with payment on; present the development setup to the page and answer the
  // purchase locally. The backend side of test purchases is covered by the Go tests.
  await page.route('**/api/v1/meta', async (route) => {
    const response = await route.fetch();
    const json = await response.json();
    await route.fulfill({
      response,
      json: { ...json, payment_enabled: false, test_purchase_enabled: true },
    });
  });
  const bodies: Record<string, unknown>[] = [];
  await page.route('**/api/v1/orders/test-purchase', async (route) => {
    bodies.push(JSON.parse(route.request().postData() ?? '{}'));
    await route.fulfill({
      status: 400,
      json: { error: { code: 'stub', message: '测试桩：未开通' } },
    });
  });
  await page.goto('/plans?cycle=yearly');
  await page.getByRole('button', { name: `测试购买 ${monthName}（年付）`, exact: true }).click();
  await expect(dialog(page).getByText('确认测试购买（年付）')).toBeVisible();
  await expect(dialog(page).getByText(`${monthName}（年付）`)).toBeVisible();
  await expect(dialog(page).getByText('120 GiB · 365 天')).toBeVisible();
  await expect(dialog(page).getByLabel('演示金额')).toHaveText('¥270.00');
  await dialog(page).getByRole('button', { name: '确认模拟开通' }).click();
  await expect(dialog(page).getByText('测试桩：未开通')).toBeVisible();
  expect(bodies.at(-1)).toEqual({ plan_id: month, billing_cycle: 'yearly' });
  await page.keyboard.press('Escape');

  await cycleRadio(page, '月付').click();
  await page.getByRole('button', { name: `测试购买 ${monthName}`, exact: true }).click();
  await expect(dialog(page).getByLabel('演示金额')).toHaveText('¥25.00');
  await dialog(page).getByRole('button', { name: '确认模拟开通' }).click();
  await expect(dialog(page).getByText('测试桩：未开通')).toBeVisible();
  expect(bodies.at(-1)).toEqual({ plan_id: month });
});
