import { expect, test, type Browser, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import { overflowing } from './layout';

// Browser acceptance for account settings (handoff_payment_account.md 7, 8; R3-01, R3-03..R3-06).
// It runs against the real API on a private PostgreSQL, started by scripts/test-payment-web.py.
type Fixture = {
  api: string;
  password: string;
  control: string;
  emails: Record<'alice' | 'bob' | 'tester' | 'carol' | 'dave' | 'erin' | 'root', string>;
};
type AccountState = { exists: boolean; status?: string; auth_version?: number };

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
const stateOf = async (email: string) =>
  (await control('user?email=' + encodeURIComponent(email))) as AccountState;

async function login(page: Page, email: string, password = fixture.password) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
}
async function signIn(page: Page, email: string, password = fixture.password) {
  await login(page, email, password);
  await expect(page).toHaveURL(/\/dashboard$/);
}

const main = (page: Page) => page.locator('main');
async function fillPasswordForm(page: Page, current: string, next: string, confirm = next) {
  const form = main(page);
  await form.getByLabel('当前密码', { exact: true }).fill(current);
  await form.getByLabel('新密码', { exact: true }).fill(next);
  await form.getByLabel('确认新密码', { exact: true }).fill(confirm);
}
const submitChange = (page: Page) => main(page).getByRole('button', { name: '修改密码' });

async function openDeleteDialog(page: Page) {
  await page.goto('/account');
  await main(page).getByRole('button', { name: '删除账号' }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  return page.getByRole('dialog');
}
const confirmDelete = (dialog: ReturnType<Page['getByRole']>) =>
  dialog.getByRole('button', { name: /^永久删除账号/ });

test.describe.configure({ mode: 'serial' });

test('the account page lists the profile, the recent sign-ins and a danger zone', async ({
  page,
}) => {
  await signIn(page, fixture.emails.carol);
  // Reached from the sidebar.
  await page.getByRole('link', { name: '账号设置' }).click();
  await expect(page).toHaveURL(/\/account$/);
  await expect(page.getByRole('heading', { name: '账号设置', level: 1 })).toBeVisible();
  await expect(page.getByTestId('profile-email')).toHaveText(fixture.emails.carol);
  // The sign-in just made is listed; the browser runs on loopback, which is never looked up.
  const logins = page.getByTestId('recent-logins');
  await expect(logins.getByRole('row')).toHaveCount(2);
  await expect(logins.getByRole('row').nth(1)).toContainText('内网/本机');
  await expect(logins.getByRole('row').nth(1)).toContainText('网站');
  await expect(logins.getByRole('row').nth(1)).toContainText('成功');
  await expect(page.getByTestId('danger-zone')).toContainText('删除账号');
  await shot(page, 'account-page');
  // No horizontal overflow on a phone-sized viewport.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await expect(page.getByTestId('profile-email')).toBeVisible();
  expect(await overflowing(page)).toEqual([]);
});

test('changing the password: checks, a wrong current password, success keeps this browser and ends the others', async ({
  page,
  browser,
}) => {
  const email = fixture.emails.carol;
  const other = await (browser as Browser).newContext();
  const otherPage = await other.newPage();
  await signIn(otherPage, email);
  await signIn(page, email);
  await page.goto('/account');
  const next = 'A-Brand-New-Password-1';

  // Local checks use the server's byte rules and never reach the server.
  await fillPasswordForm(page, fixture.password, 'short');
  await expect(page.getByText('新密码需要 10–72 字节')).toBeVisible();
  await expect(submitChange(page)).toBeDisabled();
  await fillPasswordForm(page, fixture.password, next, next + 'x');
  await expect(page.getByText('两次输入的新密码不一致').first()).toBeVisible();
  await expect(submitChange(page)).toBeDisabled();
  await fillPasswordForm(page, fixture.password, fixture.password);
  await expect(page.getByText('新密码不能与当前密码相同')).toBeVisible();
  await expect(submitChange(page)).toBeDisabled();
  // 24 Chinese characters are 72 bytes, 25 are 75: counted in bytes, not characters.
  await fillPasswordForm(page, fixture.password, '密'.repeat(24));
  await expect(submitChange(page)).toBeEnabled();
  await fillPasswordForm(page, fixture.password, '密'.repeat(25));
  await expect(page.getByText('当前 75 字节')).toBeVisible();

  // A wrong current password is refused by the server and changes nothing.
  await fillPasswordForm(page, 'not-the-current-password', next);
  await submitChange(page).click();
  await expect(main(page).getByRole('alert')).toContainText('当前密码不正确');
  expect((await stateOf(email)).auth_version).toBe(1);

  await fillPasswordForm(page, fixture.password, next);
  await submitChange(page).click();
  await expect(main(page).getByRole('status')).toContainText('密码已修改');
  await shot(page, 'password-changed');
  expect((await stateOf(email)).auth_version).toBe(2);

  // This browser stays signed in, on a fresh session.
  await page.goto('/dashboard');
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto('/account');
  await expect(page.getByTestId('profile-email')).toHaveText(email);
  // The other browser is signed out at its next request.
  await otherPage.reload();
  await expect(otherPage).toHaveURL(/\/login/);
  await other.close();

  // The old password no longer works, the new one does.
  const fresh = await (browser as Browser).newContext();
  const freshPage = await fresh.newPage();
  await login(freshPage, email);
  await expect(freshPage.getByText('邮箱或密码不正确')).toBeVisible();
  await signIn(freshPage, email, next);
  await fresh.close();
  // The sign-ins above show in the list, failures included.
  await page.goto('/account');
  const logins = page.getByTestId('recent-logins');
  await expect(logins).toContainText('失败');
  await expect(logins).toContainText('成功');
});

test('deleting an account: an open dialog needs the countdown, the password and the exact address', async ({
  page,
}) => {
  const email = fixture.emails.dave;
  await signIn(page, email);
  const dialog = await openDeleteDialog(page);
  await expect(dialog).toContainText('此操作无法撤销');
  await expect(dialog).toContainText('全部订单记录');
  // The button counts down before it can be used, and a wrong address keeps it off.
  await expect(confirmDelete(dialog)).toBeDisabled();
  await expect(confirmDelete(dialog)).toContainText('（');
  await dialog.getByLabel('当前密码').fill(fixture.password);
  await dialog.getByLabel(/请输入完整邮箱/).fill('someone-else@example.invalid');
  await page.waitForTimeout(5500);
  await expect(confirmDelete(dialog)).toBeDisabled();
  await dialog.getByLabel(/请输入完整邮箱/).fill(email.toUpperCase());
  await expect(confirmDelete(dialog)).toBeEnabled();
  await shot(page, 'delete-dialog');

  // Cancelling discards what was typed, and the next opening starts a new countdown.
  await dialog.getByRole('button', { name: '取消' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await main(page).getByRole('button', { name: '删除账号' }).click();
  const again = page.getByRole('dialog');
  await expect(again.getByLabel('当前密码')).toHaveValue('');
  await expect(again.getByLabel(/请输入完整邮箱/)).toHaveValue('');
  await expect(confirmDelete(again)).toBeDisabled();
  expect((await stateOf(email)).status).toBe('active');

  // A wrong password is refused by the server and the account is left alone.
  await again.getByLabel('当前密码').fill('definitely-not-the-password');
  await again.getByLabel(/请输入完整邮箱/).fill(email);
  await expect(confirmDelete(again)).toBeEnabled({ timeout: 15000 });
  await confirmDelete(again).click();
  await expect(again.getByRole('alert').filter({ hasText: '当前密码不正确' })).toBeVisible();
  expect((await stateOf(email)).status).toBe('active');

  // The right one deletes: signed out, home page, no way back in, data gone after the purge.
  await again.getByLabel('当前密码').fill(fixture.password);
  await confirmDelete(again).click();
  await expect(page).toHaveURL((url) => url.pathname === '/');
  expect((await stateOf(email)).status).toBe('deleting');
  await page.goto('/dashboard');
  await expect(page).toHaveURL(/\/login/);
  await login(page, email);
  await expect(page.getByText('邮箱或密码不正确')).toBeVisible();
  expect(await control('purge', 'POST')).toMatchObject({ purged: 1, failed: 0 });
  expect(await stateOf(email)).toEqual({ exists: false });
});

test('an order still open blocks deletion until it lapses', async ({ page }) => {
  const email = fixture.emails.erin;
  await signIn(page, email);
  await control('pending-order?email=' + encodeURIComponent(email), 'POST');
  const dialog = await openDeleteDialog(page);
  await dialog.getByLabel('当前密码').fill(fixture.password);
  await dialog.getByLabel(/请输入完整邮箱/).fill(email);
  await expect(confirmDelete(dialog)).toBeEnabled({ timeout: 15000 });
  await confirmDelete(dialog).click();
  await expect(dialog.getByRole('alert').filter({ hasText: '请等待约' })).toBeVisible();
  expect((await stateOf(email)).status).toBe('active');
  await shot(page, 'delete-blocked-by-order');
  // Once the order has lapsed the same dialog succeeds.
  await control('advance?seconds=200');
  await confirmDelete(dialog).click();
  await expect(page).toHaveURL((url) => url.pathname === '/');
  expect((await stateOf(email)).status).toBe('deleting');
  expect(await control('purge', 'POST')).toMatchObject({ purged: 1, failed: 0 });
});

test('an administrator account cannot delete itself on the website', async ({ page }) => {
  await signIn(page, fixture.emails.root);
  await page.goto('/account');
  await expect(page.getByTestId('danger-zone')).toContainText('管理员账号不能在网站上自助删除');
  await expect(main(page).getByRole('button', { name: '删除账号' })).toHaveCount(0);
  // The server refuses it as well, whatever the page shows.
  const refusal = await page.evaluate(
    async ({ email, password }) => {
      const response = await fetch('/api/v1/me/account/delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password }),
      });
      return { status: response.status, body: await response.json() };
    },
    { email: fixture.emails.root, password: fixture.password },
  );
  expect(refusal.status).toBe(403);
  expect(refusal.body.error.code).toBe('admin_account_protected');
  expect((await stateOf(fixture.emails.root)).status).toBe('active');
  // The administrator's password rule is 16 bytes.
  await fillPasswordForm(page, fixture.password, 'fifteen-bytes-ok');
  await expect(submitChange(page)).toBeEnabled();
  await fillPasswordForm(page, fixture.password, 'fifteen-bytes-x');
  await expect(page.getByText('新密码需要 16–72 字节')).toBeVisible();
});
