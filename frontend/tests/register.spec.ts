import { expect, test } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';

// R2-01 against the real Go API and private PostgreSQL: the browser requests a code, the
// code is read from the fixture's capture Mailer (test-only endpoint guarded by the control
// token, never part of the production API), and registration completes with the cookies.
type Fixture = { api: string; control: string };

test('email code registration end to end through Next.js and the real API', async ({ page }) => {
  const filename = process.env.CATALOG_FIXTURE;
  test.skip(!filename, 'Requires the private catalog acceptance runner');
  if (!filename?.startsWith('/tmp/vpn-catalog-')) throw new Error('Private fixture required');
  const fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
  const control = async (path: string) => {
    const response = await fetch(fixture.api + path, {
      headers: { 'X-Acceptance-Control': fixture.control },
    });
    return { status: response.status, body: response.ok ? await response.json() : null };
  };
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const email = `reg-${randomUUID().slice(0, 12)}@gmail.com`;
  const password = '浏览器注册-Test-2026';

  await page.goto('/');
  await page.getByRole('main').getByRole('link', { name: '创建账户' }).click();
  await expect(page).toHaveURL(/\/register$/);
  await expect(page.getByText(/支持：qq\.com、.*gmail\.com/)).toBeVisible();
  await page.getByLabel('姓名 / 昵称').fill('浏览器注册');
  await page.getByLabel('邮箱地址').fill(email.toUpperCase());
  await page.getByRole('button', { name: '获取验证码' }).click();
  await expect(page.getByText('如果该邮箱可以注册')).toBeVisible();
  await expect(page.getByRole('button', { name: /秒后可重发/ })).toBeDisabled();

  let mail = await control('/fixture/mail?to=' + encodeURIComponent(email));
  for (let i = 0; mail.status === 404 && i < 50; i++) {
    await page.waitForTimeout(200);
    mail = await control('/fixture/mail?to=' + encodeURIComponent(email));
  }
  expect(mail.status).toBe(200);
  const code = mail.body.code as string;
  expect(code).toMatch(/^\d{6}$/);

  const wrong = String((Number(code) + 1) % 1000000).padStart(6, '0');
  await page.getByLabel('邮箱验证码').fill(wrong);
  await page.locator('input[type="password"]').fill(password);
  await page.getByRole('button', { name: '创建账户' }).click();
  await expect(page.getByText('验证码不正确，请检查后重新输入')).toBeVisible();
  expect((await control('/fixture/account?email=' + encodeURIComponent(email))).body.count).toBe(0);

  await page.getByLabel('邮箱验证码').fill(code);
  await page.getByLabel('记住登录状态（30 天）').check();
  await page.getByRole('button', { name: '创建账户' }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByRole('heading', { name: '还没有开通套餐' })).toBeVisible();
  const cookies = (await page.context().cookies()).filter((c) => c.name.startsWith('vpn_'));
  expect(cookies.map((c) => c.name).sort()).toEqual(['vpn_access', 'vpn_refresh']);
  expect(cookies.every((c) => c.httpOnly && c.path === '/api/v1' && c.sameSite === 'Lax')).toBe(
    true,
  );
  expect(await page.evaluate(() => localStorage.length + sessionStorage.length)).toBe(0);
  const account = (await control('/fixture/account?email=' + encodeURIComponent(email))).body;
  expect(account).toEqual({ count: 1, role: 'user', status: 'active', verified: true });

  // The new account behaves like any other: reload, sign out, sign in with the password.
  await page.reload();
  await expect(page.getByRole('heading', { name: '还没有开通套餐' })).toBeVisible();
  await page.getByRole('button', { name: '账户菜单' }).click();
  await page.getByRole('menuitem', { name: '退出登录', exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);

  // Another request for the same mailbox inside the shared cooldown is refused by the server.
  await page.context().clearCookies();
  await page.goto('/register');
  await page.getByLabel('姓名 / 昵称').fill('重复注册');
  await page.getByLabel('邮箱地址').fill(email);
  await page.getByRole('button', { name: '获取验证码' }).click();
  // Up to the full 60-second cooldown remains when the flow above finishes within a second.
  await expect(page.getByText(/发送过于频繁，请在 (0:\d\d|1:00) 后重试/)).toBeVisible();
  expect((await control('/fixture/account?email=' + encodeURIComponent(email))).body.count).toBe(1);
  expect(errors).toEqual([]);
});
