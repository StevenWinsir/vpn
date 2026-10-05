import { test, expect } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';

const artifacts = process.env.E2E_ARTIFACTS_DIR || '../artifacts/qa';

test('registration, test purchase, persistence, order history and logout', async ({
  page,
  context,
}) => {
  const email = `browser-qa-${randomUUID()}@example.invalid`;
  const password = `Test-Only-${randomUUID()}!`;
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/register');
  await page.getByLabel('姓名 / 昵称').fill('浏览器测试');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(password);
  await page.getByLabel('记住登录状态（30 天）').check();
  await page.getByRole('button', { name: '注册并进入工作台' }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByText('还没有开通套餐')).toBeVisible();
  const cookies = await context.cookies(new URL('/api/v1', page.url()).href);
  const authCookies = cookies.filter((c) => c.name.startsWith('vpn_'));
  expect(authCookies).toHaveLength(2);
  expect(authCookies.every((c) => c.httpOnly)).toBe(true);
  expect(await page.evaluate(() => localStorage.length)).toBe(0);
  await page.getByRole('link', { name: '选择套餐', exact: true }).click();
  await page.getByRole('button', { name: '测试购买 进阶计划', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button', { name: '确认模拟开通', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByRole('heading', { name: '进阶计划', exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('heading', { name: '进阶计划', exact: true })).toBeVisible();
  const refreshBefore = (await context.cookies()).find((cookie) => cookie.name === 'vpn_refresh')?.value;
  expect(refreshBefore).toBeTruthy();
  await context.clearCookies({ name: 'vpn_access' });
  const refreshed = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/auth/refresh') && response.status() === 200,
  );
  await page.reload();
  await refreshed;
  await expect(page.getByRole('heading', { name: '进阶计划', exact: true })).toBeVisible();
  const refreshedCookies = (await context.cookies()).filter((cookie) => cookie.name.startsWith('vpn_'));
  expect(refreshedCookies).toHaveLength(2);
  expect(refreshedCookies.every((cookie) => cookie.httpOnly)).toBe(true);
  expect(refreshedCookies.find((cookie) => cookie.name === 'vpn_refresh')?.value).not.toBe(refreshBefore);
  expect(await page.evaluate(() => localStorage.length)).toBe(0);
  await mkdir(artifacts, { recursive: true });
  await page.screenshot({
    path: path.join(artifacts, 'dashboard-desktop.png'),
    fullPage: true,
    animations: 'disabled',
  });
  await page.getByRole('link', { name: '订单记录', exact: true }).click();
  await expect(page.getByRole('cell', { name: '测试成功', exact: true })).toBeVisible();
  await expect(page.getByRole('cell', { name: '¥39.90', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '账户菜单' }).click();
  await page.getByRole('menuitem', { name: '退出登录', exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);
  expect((await context.cookies()).filter((cookie) => cookie.name.startsWith('vpn_'))).toHaveLength(0);
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill('not-the-right-password');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page.getByText('邮箱或密码不正确')).toBeVisible();
  await page.locator('input[type="password"]').fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByRole('heading', { name: '进阶计划', exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('main')).toHaveCSS('padding-left', '0px');
  await expect
    .poll(async () => (await page.locator('.subscription-banner').boundingBox())?.width || 0)
    .toBeGreaterThan(330);
  await page.screenshot({
    path: path.join(artifacts, 'dashboard-mobile.png'),
    fullPage: true,
    animations: 'disabled',
  });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.getByRole('button', { name: '展开导航' }).click();
  await page.getByRole('link', { name: '套餐与订阅', exact: true }).click();
  await expect(page.getByRole('heading', { name: '找到适合你的连接计划' })).toBeVisible();
  await expect(page.getByRole('button', { name: '测试购买 进阶计划', exact: true })).toBeVisible();
  await expect(page.getByRole('main')).toHaveCSS('padding-left', '0px');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({
    path: path.join(artifacts, 'plans-mobile.png'),
    fullPage: true,
    animations: 'disabled',
  });
  expect(errors).toEqual([]);
});

test('public pages fit mobile and protected routes redirect', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  for (const route of ['/', '/login', '/register', '/plans']) {
    await page.goto(route);
    await expect(page.locator('h1')).toBeVisible();
    if (route === '/plans')
      await expect(
        page.getByRole('button', { name: '测试购买 进阶计划', exact: true }),
      ).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true);
  }
  await page.goto('/orders');
  await expect(page).toHaveURL(/\/login$/);
});
