import { expect, test, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import { mkdir, readFile } from 'node:fs/promises';

// Browser acceptance for the website payment experience. The backend is the real API with the
// payment gateway enabled; the gateway is a local fake written from 支付接入技术文档.md
// (payment_harness_test.go). Nothing here proves the real gateway: that is checklist U-01..U-09.
type Fixture = {
  api: string;
  gateway: string;
  password: string;
  control: string;
  emails: { alice: string; bob: string; tester: string };
};
type FixtureOrder = {
  id: string;
  trade_no: string;
  status: string;
  pay_type: string;
  amount_cents: number;
  paid_cents: number | null;
  price_source: string;
  coupon_code: string;
};
// Evidence screenshots; they carry no secrets (the gateway URL is never on a page).
async function shot(page: Page, name: string) {
  const dir = `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${name}.png`, animations: 'disabled' });
}
const secret = 'test-only-gateway-secret';
const channel = '1001';

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
const advance = (seconds: number) => control('advance?seconds=' + seconds);
const pay = (tradeNo: string, money?: number) =>
  control(`pay?trade_no=${tradeNo}${money === undefined ? '' : '&money=' + money}`, 'POST');

async function login(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

async function openDialog(page: Page, planName: string, verb = '购买') {
  await page.goto('/plans');
  await page.getByRole('button', { name: `${verb} ${planName}`, exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
}

const choose = (page: Page, name: '微信支付' | '支付宝') =>
  page.getByRole('radio', { name }).click();
const goPay = (page: Page) => page.getByRole('button', { name: '去支付', exact: true });
async function apply(page: Page, code: string) {
  await page.getByLabel('优惠码（可留空）').fill(code);
  await page.getByRole('button', { name: '应用', exact: true }).click();
}

test.describe.configure({ mode: 'serial' });

test('wechat checkout with a coupon redirects to the gateway, pays by callback and lands on the result page', async ({
  page,
}) => {
  await login(page, fixture.emails.alice);
  await openDialog(page, '进阶计划');
  // Nothing is preselected, and the dialog shows the server's quote: no payment method, no way to pay.
  await expect(page.getByRole('radio', { name: '微信支付' })).toHaveAttribute(
    'aria-checked',
    'false',
  );
  await expect(page.getByRole('radio', { name: '支付宝' })).toHaveAttribute(
    'aria-checked',
    'false',
  );
  await expect(goPay(page)).toBeDisabled();
  await expect(page.getByLabel('应付金额')).toHaveText('¥39.90');
  await choose(page, '微信支付');
  await expect(goPay(page)).toBeEnabled();
  // A typed but unapplied coupon blocks payment: the price on screen must be the one charged.
  await page.getByLabel('优惠码（可留空）').fill('save20');
  await expect(goPay(page)).toBeDisabled();
  await apply(page, 'save20');
  await expect(page.getByText('优惠码 SAVE20 已应用')).toBeVisible();
  await expect(page.getByLabel('应付金额')).toHaveText('¥31.92');
  await expect(goPay(page)).toBeEnabled();
  await shot(page, 'checkout-dialog');
  await goPay(page).click();

  // R2-05: the gateway URL is exactly the documented request.
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  const order = await lastOrder(fixture.emails.alice);
  expect(order).toMatchObject({
    status: 'pending',
    pay_type: 'wxpay',
    amount_cents: 3192,
    coupon_code: 'SAVE20',
  });
  const raw = new URL(page.url()).search.slice(1);
  const enc = encodeURIComponent;
  const returnUrl = `${new URL(page.url()).searchParams.get('return_url')}`;
  expect(returnUrl).toMatch(
    new RegExp(`^http://127\\.0\\.0\\.1:\\d+/pay/result\\?order=${order.id}$`),
  );
  const expected = [
    ['channel', channel],
    ['type', 'wxpay'],
    ['trade_no', order.trade_no],
    ['notify_url', 'https://api.pay-test.example/api/v1/payments/notify'],
    ['return_url', returnUrl],
    ['name', '进阶计划'],
    ['money', '31.92'],
    [
      'sign',
      createHash('md5')
        .update(order.trade_no + secret)
        .digest('hex'),
    ],
  ]
    .map(([key, value]) => `${key}=${enc(value)}`)
    .join('&');
  expect(raw).toBe(expected);
  expect(order.trade_no).toMatch(/^\d{20}$/);
  await expect(page.locator('#amount')).toHaveText('31.92');

  // The customer pays; the gateway calls back and then returns the browser after 5 seconds.
  await pay(order.trade_no);
  await page.waitForURL(/\/pay\/result\?order=/, { timeout: 30000 });
  await expect(page.getByRole('heading', { name: '已到账', exact: true })).toBeVisible();
  await expect(page.getByText('进阶计划', { exact: true })).toBeVisible();
  await shot(page, 'result-paid');

  // The order page and the overview agree.
  await page.getByRole('link', { name: '订单记录', exact: true }).first().click();
  const row = page.getByRole('row').filter({ hasText: order.id.slice(0, 8) });
  await expect(row.getByText('已支付', { exact: true })).toBeVisible();
  await expect(row.getByText('微信支付', { exact: true })).toBeVisible();
  await expect(row.getByText('¥31.92')).toBeVisible();
  await expect(row.getByText('优惠 ¥7.98（SAVE20）')).toBeVisible();
  await page.goto('/dashboard');
  await expect(page.getByRole('heading', { name: '进阶计划', exact: true })).toBeVisible();

  // The same coupon cannot be used twice by one account; renewing the plan is offered as 续费.
  await openDialog(page, '进阶计划', '续费');
  await apply(page, 'SAVE20');
  await expect(page.getByText('你已使用过此优惠码')).toBeVisible();
  await expect(goPay(page)).toBeDisabled();
  // Other plans cannot be bought during the validity period.
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: '购买 轻量计划', exact: true })).toBeDisabled();
});

test('payment method and coupon rules in the dialog; a session that ends closes it', async ({
  page,
}) => {
  await login(page, fixture.emails.bob);
  await openDialog(page, '轻量计划');
  await expect(page.getByLabel('应付金额')).toHaveText('¥19.90');
  await expect(goPay(page)).toBeDisabled();
  // Empty coupon is fine; each of the four failure reasons is spelled out.
  for (const [code, reason] of [
    ['OLDCODE', '优惠码无效或已失效'],
    ['nosuchcode', '优惠码无效或已失效'],
    ['ab', '优惠码无效或已失效'],
    ['ONLYMAX', '此优惠码不适用于所选套餐'],
    ['USEDUP', '此优惠码已达使用上限'],
  ] as const) {
    await apply(page, code);
    await expect(page.getByRole('alert').filter({ hasText: reason })).toBeVisible();
    await expect(page.getByLabel('应付金额')).toHaveText('¥19.90');
    await choose(page, '支付宝');
    await expect(goPay(page)).toBeDisabled();
  }
  await apply(page, 'SAVE20');
  await expect(page.getByLabel('应付金额')).toHaveText('¥15.92');
  await expect(goPay(page)).toBeEnabled();
  // The session ends underneath the dialog: it must disappear instead of submitting for anyone.
  await page.context().clearCookies();
  await apply(page, 'OLDCODE');
  await expect(page.getByRole('dialog')).toBeHidden();
});

test('the test price is flagged for listed accounts only', async ({ page, browser }) => {
  await login(page, fixture.emails.tester);
  await openDialog(page, '轻量计划');
  await expect(page.getByText('测试价：当前账户按测试价结算，会产生真实扣款。')).toBeVisible();
  await expect(page.getByLabel('应付金额')).toHaveText('¥0.01');
  const other = await browser.newPage();
  await login(other, fixture.emails.bob);
  await openDialog(other, '轻量计划');
  await expect(other.getByText('测试价')).toHaveCount(0);
  await other.close();
});

test('result pages only trust the order state, and other accounts cannot see an order', async ({
  page,
  browser,
}) => {
  await login(page, fixture.emails.bob);
  await openDialog(page, '轻量计划');
  await choose(page, '支付宝');
  await goPay(page).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  const bobs = await lastOrder(fixture.emails.bob);

  const alice = await browser.newPage();
  await login(alice, fixture.emails.alice);
  // R2-03: somebody else's order is not found, whatever the address says.
  await alice.goto(`/pay/result?order=${bobs.id}&status=success`);
  await expect(alice.getByRole('heading', { name: '订单不存在' })).toBeVisible();
  await alice.goto('/pay/result?order=not-an-id');
  await expect(alice.getByRole('heading', { name: '订单不存在' })).toBeVisible();

  // R2-03: forcing "success" into the address does not change an unpaid order.
  await page.goto(`/pay/result?order=${bobs.id}&status=success&trade_status=TRADE_SUCCESS`);
  await expect(page.getByRole('heading', { name: '待支付', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: '已到账' })).toHaveCount(0);
  expect((await lastOrder(fixture.emails.bob)).status).toBe('pending');
  await alice.close();

  // Signed out, the result page goes through sign-in and comes back to the same order.
  await page.context().clearCookies();
  await page.goto(`/pay/result?order=${bobs.id}`);
  await expect(page).toHaveURL(/\/login\?next=/);
  await page.getByLabel('邮箱地址').fill(fixture.emails.bob);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/pay/result\\?order=${bobs.id}$`));
  await expect(page.getByRole('heading', { name: '待支付', exact: true })).toBeVisible();
  // A next target that is not a result page is ignored.
  await page.context().clearCookies();
  await page.goto('/login?next=' + encodeURIComponent('https://evil.example/'));
  await page.getByLabel('邮箱地址').fill(fixture.emails.bob);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
});

test('timeout page returns to an expired order, buy again preselects, and a late callback still opens the plan', async ({
  page,
}) => {
  await login(page, fixture.emails.bob);
  await openDialog(page, '进阶计划');
  await choose(page, '微信支付');
  await goPay(page).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  const order = await lastOrder(fixture.emails.bob);
  // Two minutes pass at the gateway; its timeout page offers the way back.
  await advance(125);
  await expect(page.locator('#state')).toHaveText('timeout');
  await page.getByRole('link', { name: '返回商户页面' }).click();
  await page.waitForURL(/\/pay\/result\?order=/);
  await expect(page.getByRole('heading', { name: '订单已超时，未扣款' })).toBeVisible();
  // Buy again returns to the plans page with the plan and method preselected.
  await page.getByRole('link', { name: '重新购买' }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await expect(page.getByRole('radio', { name: '微信支付' })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await expect(page.getByRole('heading', { name: '购买 进阶计划' })).toBeVisible();
  await page.keyboard.press('Escape');

  // A trusted callback after the timeout still opens the subscription (6.3).
  await pay(order.trade_no);
  await page.goto('/orders');
  await expect(
    page
      .getByRole('row')
      .filter({ hasText: order.id.slice(0, 8) })
      .getByText('已支付', { exact: true }),
  ).toBeVisible();
  await page.goto('/dashboard');
  await expect(page.getByRole('heading', { name: '进阶计划', exact: true })).toBeVisible();
});

test('closing the gateway window leaves a countdown, allows buying again, then the order times out', async ({
  page,
}) => {
  await login(page, fixture.emails.tester);
  const buy = async () => {
    await openDialog(page, '轻量计划');
    await choose(page, '支付宝');
    await goPay(page).click();
    await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  };
  await buy();
  const first = await lastOrder(fixture.emails.tester);
  // The customer closes the gateway tab: only the website is left.
  await page.goto('/orders');
  await expect(page.getByText(/剩余 \d:\d\d/)).toBeVisible();
  // Buying the same plan again is allowed; the gateway separates the amounts by a cent.
  await buy();
  const second = await lastOrder(fixture.emails.tester);
  expect(second.id).not.toBe(first.id);
  await expect(page.locator('#amount')).toHaveText('0.02');
  await page.goto('/orders');
  await expect(page.getByText('你有多笔未完成的订单')).toBeVisible();
  // Past the local window the reconciler settles both and the list follows.
  await advance(200);
  for (const id of [first.id, second.id])
    await expect(
      page
        .getByRole('row')
        .filter({ hasText: id.slice(0, 8) })
        .getByText('已超时', { exact: true }),
    ).toBeVisible({
      timeout: 40000,
    });
  expect((await ordersOf(fixture.emails.tester)).map((o) => o.status)).toEqual([
    'expired',
    'expired',
  ]);
});

test('an underpaid callback is held for review and the result page says so', async ({ page }) => {
  await login(page, fixture.emails.alice);
  await openDialog(page, '进阶计划', '续费');
  await choose(page, '支付宝');
  await goPay(page).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  const order = await lastOrder(fixture.emails.alice);
  const before = await control('subscription?email=' + encodeURIComponent(fixture.emails.alice));
  // The customer edited the amount to 1.00 before paying: the callback carries 1.00.
  await pay(order.trade_no, 100);
  await page.waitForURL(/\/pay\/result\?order=/, { timeout: 30000 });
  await expect(page.getByRole('heading', { name: '待人工核对' })).toBeVisible();
  await shot(page, 'result-review');
  await expect(page.getByText(`订单号 ${order.id}`)).toBeVisible();
  expect(await control('subscription?email=' + encodeURIComponent(fixture.emails.alice))).toEqual(
    before,
  );
  expect((await lastOrder(fixture.emails.alice)).status).toBe('review');
  await page.goto('/orders');
  await expect(
    page
      .getByRole('row')
      .filter({ hasText: order.id.slice(0, 8) })
      .getByText('待人工核对', { exact: true }),
  ).toBeVisible();
});
