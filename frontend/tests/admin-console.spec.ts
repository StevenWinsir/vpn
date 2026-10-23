import { expect, test, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';

// Browser acceptance for the administrator console, part one (handoff_payment_account.md 9;
// R4-01, R4-02, R4-03, R4-05, R4-07). It runs against the real API on a private PostgreSQL,
// started by scripts/test-payment-web.py.
type Fixture = {
  api: string;
  password: string;
  control: string;
  emails: Record<'alice' | 'carol' | 'frank' | 'gina' | 'hank' | 'root', string>;
};

async function shot(page: Page, name: string) {
  const dir = `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${name}.png`, animations: 'disabled' });
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

async function signIn(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

const drawer = (page: Page) => page.getByRole('dialog', { name: /^(用户|订单)详情/ });
test.describe.configure({ mode: 'serial' });

test('an ordinary user sees no console and is refused by it', async ({ page }) => {
  await signIn(page, fixture.emails.alice);
  await expect(page.getByRole('link', { name: '管理后台' })).toHaveCount(0);
  await page.goto('/admin/console/users');
  await expect(page.getByText('此页面仅对管理员开放')).toBeVisible();
  const status = await page.evaluate(
    async () => (await fetch('/api/v1/admin/console/users')).status,
  );
  expect(status).toBe(403);
});

test('an administrator filters users, opens one and changes the plan; the user sees it', async ({
  page,
  browser,
}) => {
  await signIn(page, fixture.emails.root);
  // The console is its own entry, next to node management.
  await expect(page.getByRole('link', { name: '管理后台' })).toBeVisible();
  await expect(page.getByRole('link', { name: '节点管理' })).toBeVisible();
  await page.getByRole('link', { name: '管理后台' }).click();
  await expect(page).toHaveURL(/\/admin\/console\/users$/);
  await expect(page.getByTestId('user-row').first()).toBeVisible();

  // Keyword filter, kept in the address.
  const form = page.getByRole('form', { name: '用户筛选' });
  await form.getByLabel('邮箱或姓名').fill(fixture.emails.frank);
  await form.getByRole('button', { name: '筛选' }).click();
  await expect(page).toHaveURL(
    new RegExp('q=' + encodeURIComponent(fixture.emails.frank).replace(/\./g, '\\.')),
  );
  await expect(page.getByTestId('user-row')).toHaveCount(1);
  await page.reload();
  await expect(page.getByTestId('user-row')).toHaveCount(1);
  await expect(page.getByTestId('user-row')).toContainText(fixture.emails.frank);
  // LIKE wildcards are literal.
  await form.getByLabel('邮箱或姓名').fill('%');
  await form.getByRole('button', { name: '筛选' }).click();
  await expect(page.getByText('没有符合条件的用户')).toBeVisible();
  await form.getByLabel('邮箱或姓名').fill('frank');
  await form.getByRole('button', { name: '筛选' }).click();
  await expect(page.getByTestId('user-row')).toHaveCount(1);
  await shot(page, 'console-users');

  // Detail drawer.
  await page.getByRole('button', { name: `查看 ${fixture.emails.frank}` }).click();
  await expect(drawer(page)).toContainText('该用户当前没有订阅');
  await drawer(page).getByRole('button', { name: '修改套餐' }).click();
  const modal = page.getByRole('dialog', { name: /^修改套餐/ });
  await modal.getByLabel('套餐', { exact: true }).click();
  await page.getByRole('option', { name: /进阶计划/ }).click();
  await modal.getByLabel('到期时间').fill('2031-01-15T10:00');
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(drawer(page).getByTestId('subscription-block')).toContainText('进阶计划');
  await expect(drawer(page).getByTestId('subscription-block')).toContainText('有效');
  await shot(page, 'console-user-detail');

  // The user's own overview follows.
  const other = await browser.newContext();
  const userPage = await other.newPage();
  await signIn(userPage, fixture.emails.frank);
  await expect(userPage.getByText('进阶计划').first()).toBeVisible();
  await other.close();

  // Reset usage and end the subscription from the same drawer.
  await drawer(page).getByRole('button', { name: '结束订阅' }).click();
  await page
    .getByRole('dialog', { name: '结束订阅' })
    .getByRole('button', { name: '确认结束' })
    .click();
  await expect(drawer(page).getByTestId('subscription-block')).toContainText('已过期');
});

test('a stale plan form loses to a newer change', async ({ page, browser }) => {
  await signIn(page, fixture.emails.root);
  await page.goto('/admin/console/users?q=' + encodeURIComponent(fixture.emails.frank));
  await page.getByRole('button', { name: `查看 ${fixture.emails.frank}` }).click();
  await drawer(page).getByRole('button', { name: '修改套餐' }).click();
  const modal = page.getByRole('dialog', { name: /^修改套餐/ });
  // Meanwhile another administrator session saves a change.
  const rival = await browser.newContext();
  const rivalPage = await rival.newPage();
  await signIn(rivalPage, fixture.emails.root);
  await rivalPage.goto('/admin/console/users?q=' + encodeURIComponent(fixture.emails.frank));
  await rivalPage.getByRole('button', { name: `查看 ${fixture.emails.frank}` }).click();
  await rivalPage
    .getByRole('dialog', { name: /^用户详情/ })
    .getByRole('button', { name: '修改套餐' })
    .click();
  const rivalModal = rivalPage.getByRole('dialog', { name: /^修改套餐/ });
  await rivalModal.getByLabel('到期时间').fill('2032-01-01T10:00');
  await rivalModal.getByLabel('总流量（GiB）').fill('7');
  await rivalModal.getByRole('button', { name: '保存' }).click();
  await expect(rivalPage.getByText('套餐已保存')).toBeVisible();
  await rival.close();
  await modal.getByLabel('总流量（GiB）').fill('9');
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(modal.getByTestId('console-error')).toContainText('刚刚被其他操作修改');
  await expect(modal.getByRole('button', { name: '刷新最新数据' })).toBeVisible();
});

test('the order list confirms a payment under review once, after the second factor', async ({
  page,
}) => {
  const order = (await control(
    'review-order?email=' + encodeURIComponent(fixture.emails.hank),
  )) as {
    id: string;
    trade_no: string;
  };
  await signIn(page, fixture.emails.root);
  await page.goto('/admin/console/orders?status=review');
  const row = page.getByTestId('order-row').filter({ hasText: order.trade_no });
  await expect(row).toContainText('待人工核对');
  await expect(page.getByTestId('summary-today')).toBeVisible();
  await page.getByRole('button', { name: `查看订单 ${order.trade_no}` }).click();
  // The callback ledger shows where the callback came from (U-01/U-02).
  await expect(page.getByTestId('notify-ip')).toHaveText('203.0.113.77');
  await shot(page, 'console-order-detail');
  await page.getByRole('button', { name: '人工确认到账' }).click();
  const modal = page.getByRole('dialog', { name: '人工确认到账' });
  await modal.getByLabel('商户后台核实的实付金额（元）').fill('39.9');
  await modal.getByLabel('核实备注').fill('商户后台已核实');
  await modal.getByLabel('管理员密码').fill('wrong-' + fixture.password);
  await modal.getByRole('button', { name: '确认到账并开通' }).click();
  await expect(modal.getByTestId('console-error')).toContainText('管理员密码不正确');
  expect(
    (
      (await control('orders?email=' + encodeURIComponent(fixture.emails.hank))) as {
        status: string;
      }[]
    )[0].status,
  ).toBe('review');
  await modal.getByLabel('管理员密码').fill(fixture.password);
  await modal.getByRole('button', { name: '确认到账并开通' }).click();
  await expect(page.getByText('已确认到账')).toBeVisible();
  await expect(row).toHaveCount(0); // no longer under review
  const orders = (await control('orders?email=' + encodeURIComponent(fixture.emails.hank))) as {
    status: string;
    paid_cents: number;
  }[];
  expect(orders[0]).toMatchObject({ status: 'paid', paid_cents: 3990 });
  const sub = (await control('subscription?email=' + encodeURIComponent(fixture.emails.hank))) as {
    plan_id: string;
  };
  expect(sub.plan_id).toBe('pro');
});

test('the sign-in panel lists attempts and filters them', async ({ page }) => {
  await signIn(page, fixture.emails.root);
  await page.goto('/admin/console/logins');
  await expect(page.getByTestId('login-row').first()).toBeVisible();
  await expect(
    page.getByTestId('login-row').filter({ hasText: fixture.emails.root }).first(),
  ).toContainText('成功');
  const form = page.getByRole('form', { name: '登录记录筛选' });
  await form.getByLabel('用户邮箱').fill(fixture.emails.root);
  await form.getByRole('button', { name: '筛选' }).click();
  await expect(page).toHaveURL(/email=/);
  for (const row of await page.getByTestId('login-row').all())
    await expect(row).toContainText(fixture.emails.root);
  await page.getByText('短时间多次失败').click();
  await expect(page).toHaveURL(/quick=burst_failures/);
  await shot(page, 'console-logins');
});

test('deleting a user needs the email and the administrator password', async ({ page }) => {
  await signIn(page, fixture.emails.root);
  await page.goto('/admin/console/users?q=' + encodeURIComponent(fixture.emails.gina));
  await page.getByRole('button', { name: `查看 ${fixture.emails.gina}` }).click();
  await drawer(page).getByRole('button', { name: '删除用户' }).click();
  const modal = page.getByRole('dialog', { name: '删除用户' });
  const confirm = modal.getByRole('button', { name: '永久删除' });
  await expect(confirm).toBeDisabled();
  await modal.getByLabel(/输入目标邮箱/).fill(fixture.emails.gina);
  await modal.getByLabel('管理员密码').fill(fixture.password);
  await confirm.click();
  await expect(page.getByText('账号已删除')).toBeVisible();
  const state = (await control('user?email=' + encodeURIComponent(fixture.emails.gina))) as {
    exists: boolean;
    status?: string;
  };
  expect(!state.exists || state.status === 'deleting').toBe(true);
  // Administrators are protected from these actions.
  await page.goto('/admin/console/users?role=admin');
  await page.getByRole('button', { name: `查看 ${fixture.emails.root}` }).click();
  await expect(drawer(page).getByRole('button', { name: '删除用户' })).toBeDisabled();
  await expect(drawer(page).getByRole('button', { name: '修改密码' })).toBeDisabled();
});
