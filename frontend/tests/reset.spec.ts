import { expect, test } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';

// R3-01/02/04/09 against the real Go API and private PostgreSQL: a historical account signed
// in on the website and in a client asks for a link and opens it in another tab of the same,
// still signed-in browser, then sets a new password; every old session ends, this browser's
// cookies are cleared, the old password stops working and the link dies after use.
// Mail is read from the fixture's capture Mailer through the control-token endpoint only.
type Fixture = { api: string; control: string; password: string };

test('mail-link password reset end to end through Next.js and the real API', async ({ page }) => {
  const filename = process.env.CATALOG_FIXTURE;
  test.skip(!filename, 'Requires the private catalog acceptance runner');
  if (!filename?.startsWith('/tmp/vpn-catalog-')) throw new Error('Private fixture required');
  const fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
  const control = async (path: string, method = 'GET') => {
    const response = await fetch(fixture.api + path, {
      method,
      headers: { 'X-Acceptance-Control': fixture.control },
    });
    return {
      status: response.status,
      body: response.status === 200 ? await response.json() : null,
    };
  };
  const client = (path: string, init: RequestInit = {}) =>
    fetch(fixture.api + '/api/v1/client' + path, {
      ...init,
      headers: { 'Content-Type': 'application/json', ...init.headers },
    });
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const email = `reset-${randomUUID().slice(0, 12)}@example.invalid`;
  const newPassword = '重置后的新密码-2026';
  expect((await control('/fixture/historical-user?email=' + email, 'POST')).status).toBe(201);

  // Signed in on the website and in a client.
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  const native = await client('/login', {
    method: 'POST',
    body: JSON.stringify({
      email,
      password: fixture.password,
      device_id: randomUUID(),
      platform: 'macos',
      app_version: 'reset-acceptance',
    }),
  });
  expect(native.status).toBe(200);
  const { token: nativeToken } = (await native.json()) as { token: string };
  const nativeSession = () =>
    client('/session', { headers: { Authorization: 'Bearer ' + nativeToken } });
  expect((await nativeSession()).status).toBe(200);

  // A second tab of the signed-in browser asks for a link and opens it: the public form
  // works whatever the signed-in state is.
  const phone = await page.context().newPage();
  phone.on('pageerror', (error) => errors.push(error.message));
  const urls: string[] = [];
  phone.on('request', (request) => urls.push(request.url()));
  await phone.goto('/login');
  await phone.getByRole('link', { name: '忘记密码？' }).click();
  await expect(phone).toHaveURL(/\/forgot-password$/);
  await phone.getByLabel('邮箱地址').fill(email.toUpperCase());
  await phone.getByRole('button', { name: '发送重置链接' }).click();
  await expect(phone.getByText('如果该邮箱已注册')).toBeVisible();
  let link = await control('/fixture/reset-link?to=' + encodeURIComponent(email));
  for (let i = 0; link.status === 404 && i < 50; i++) {
    await phone.waitForTimeout(200);
    link = await control('/fixture/reset-link?to=' + encodeURIComponent(email));
  }
  expect(link.status).toBe(200);
  const token = link.body.token as string;

  // Opening the mail, prefetching and validating do not consume the link.
  await phone.goto('/reset-password#token=' + token);
  await expect(phone.getByLabel(/^新密码/)).toBeVisible();
  expect(new URL(phone.url()).hash).toBe('');
  await phone.goto('/login');
  await phone.goto('/reset-password#token=' + token);
  await expect(phone.getByLabel(/^新密码/)).toBeVisible();
  expect((await nativeSession()).status).toBe(200);

  await phone.getByLabel(/^新密码/).fill(fixture.password);
  await phone.getByLabel('确认新密码').fill(fixture.password);
  await phone.getByRole('button', { name: '设置新密码' }).click();
  await expect(phone.getByText('新密码不能与当前密码相同')).toBeVisible();
  await phone.getByLabel(/^新密码/).fill(newPassword);
  await phone.getByLabel('确认新密码').fill(newPassword);
  await phone.getByRole('button', { name: '设置新密码' }).click();
  await expect(phone.getByText('密码已重置')).toBeVisible();
  expect(urls.every((url) => !url.includes(token))).toBe(true);
  // The reset cleared this browser's authentication cookies and signed nobody in.
  expect((await page.context().cookies()).filter((c) => c.name.startsWith('vpn_'))).toEqual([]);

  // Every old session is over: the client gets the code it already treats as the end.
  const ended = await nativeSession();
  expect(ended.status).toBe(401);
  expect(((await ended.json()) as { error: { code: string } }).error.code).toBe(
    'native_session_expired',
  );
  // The dashboard tab finds out on its next protected request and lands on the login page.
  await page.getByRole('link', { name: '订单记录', exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);

  // The link is dead; the old password fails; the new one signs into the same account.
  await phone.goto('/reset-password#token=' + token);
  await expect(phone.getByText('重置链接无效或已过期')).toBeVisible();
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page.getByText('邮箱或密码不正确')).toBeVisible();
  await page.locator('input[type="password"]').fill(newPassword);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  // Holding the mailed link proved the mailbox of this historical account.
  const account = (await control('/fixture/account?email=' + encodeURIComponent(email))).body;
  expect(account).toEqual({ count: 1, role: 'user', status: 'active', verified: true });
  expect(await phone.evaluate(() => localStorage.length + sessionStorage.length)).toBe(0);
  await phone.close();
  expect(errors).toEqual([]);
});
