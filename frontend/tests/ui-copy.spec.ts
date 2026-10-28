import { expect, test, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import { bannedIn } from './copy-words';

// Runtime copy guard for the user pages (handoff_ui.md 4.6, round 1). It runs against the real API
// with the payment gateway on, which is what production looks like, and reads what a customer
// actually sees: no development wording, none of the four removed items, a title on every page.
type Fixture = {
  api: string;
  gateway: string;
  password: string;
  control: string;
  emails: { copya: string; copyb: string };
};
type FixtureOrder = { id: string; trade_no: string; status: string };

const removed = [
  '推荐有礼 · 我的推荐码',
  '线路信息，与管理员配置同步',
  '支持微信支付与支付宝支付',
  '开发联调版',
];

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

async function login(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

// Visible text once the page has settled: skeletons gone and the main heading shown.
async function visibleText(page: Page) {
  await expect(page.locator('h1').first()).toBeVisible();
  await expect(page.locator('.mantine-Skeleton-root')).toHaveCount(0);
  return page.evaluate(() => document.body.innerText);
}

async function check(page: Page, path: string, title: RegExp, screenshot?: string) {
  await page.goto(path);
  const text = await visibleText(page);
  const words = bannedIn(text);
  expect(words, `${path} shows ${words.join(', ')}`).toEqual([]);
  for (const phrase of removed) expect(text, path).not.toContain(phrase);
  await expect(page).toHaveTitle(title);
  if (screenshot) await shot(page, screenshot);
}

test.describe.configure({ mode: 'serial' });

test('public pages carry commercial copy only', async ({ page }) => {
  await check(page, '/', /^AsterLink/);
  await check(page, '/login', /^登录 · /);
  await check(page, '/register', /^注册 · /);
  await check(page, '/forgot-password', /^找回密码 · /);
  await check(page, '/plans', /^套餐与订阅 · /);
  await check(page, '/download', /^下载客户端 · /);
  await check(page, '/no-such-page', /^页面不存在 · /);
});

test('a customer without a plan sees no development wording and none of the removed items', async ({
  page,
}) => {
  await login(page, fixture.emails.copya);
  await expect(page.getByRole('heading', { name: '还没有开通套餐' })).toBeVisible();
  await expect(page.getByTestId('referral-card')).toHaveCount(0);
  await check(page, '/dashboard', /^总览 · /, 'ui-copy-dashboard-no-plan');
  await check(page, '/nodes', /^节点列表 · /);
  await check(page, '/plans', /^套餐与订阅 · /, 'ui-copy-plans');
  await check(page, '/orders', /^订单记录 · /);
  await check(page, '/referral', /^推荐有礼 · /);
  await check(page, '/download', /^下载客户端 · /);
  await check(page, '/account', /^账号设置 · /);
  // The sidebar ends with the online chat and the way home (handoff_ui.md 5.4); the development
  // note is gone.
  await expect(page.locator('.sidebar-note')).toHaveText(/^在线客服返回首页\s*$/);
});

test('a paying customer sees the same, including the overview, orders and payment result', async ({
  page,
}) => {
  await login(page, fixture.emails.copyb);
  await page.goto('/plans');
  await page.getByRole('button', { name: '购买 轻量计划', exact: true }).click();
  await page.getByRole('radio', { name: '支付宝' }).click();
  await page.getByRole('button', { name: '去支付', exact: true }).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  const orders = (await control(
    'orders?email=' + encodeURIComponent(fixture.emails.copyb),
  )) as FixtureOrder[];
  const order = orders.at(-1)!;
  await control(`pay?trade_no=${order.trade_no}`, 'POST');
  await page.waitForURL(/\/pay\/result\?order=/, { timeout: 30000 });
  await expect(page.getByRole('heading', { name: '已到账', exact: true })).toBeVisible();
  await check(page, `/pay/result?order=${order.id}`, /^支付结果 · /);

  await check(page, '/dashboard', /^总览 · /, 'ui-copy-dashboard');
  await expect(page.getByRole('heading', { name: '轻量计划', exact: true })).toBeVisible();
  await expect(page.getByTestId('referral-card')).toHaveCount(0);
  // Traffic reads in GB on user pages (handoff_ui.md D6).
  await expect(page.getByText('剩余流量')).toBeVisible();
  expect(await page.evaluate(() => document.body.innerText)).not.toContain('GiB');
  await check(page, '/nodes', /^节点列表 · /, 'ui-copy-nodes');
  await check(page, '/plans', /^套餐与订阅 · /);
  await check(page, '/orders', /^订单记录 · /);
  await expect(page.getByText('已支付', { exact: true }).first()).toBeVisible();
  await check(page, '/referral', /^推荐有礼 · /);
  await check(page, '/account', /^账号设置 · /);
});
