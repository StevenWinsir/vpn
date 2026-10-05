import { expect, test, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';

test('administrator YAML catalog persists and ordinary users cannot access it', async ({
  page,
  browser,
}) => {
  const fixturePath = process.env.CATALOG_FIXTURE;
  const output = process.env.CATALOG_OUTPUTS;
  test.skip(!fixturePath || !output, 'Requires the private catalog acceptance runner');
  if (!fixturePath?.startsWith('/tmp/vpn-catalog-') || !output)
    throw new Error('Private fixture required');
  const fixture = JSON.parse(await readFile(fixturePath, 'utf8')) as {
    emails: { admin: string; vip: string };
    password: string;
    node_yaml: string;
  };
  await mkdir(output, { recursive: true });
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));

  async function login(target: Page, email: string) {
    await target.goto('/login');
    await target.getByLabel('邮箱地址').fill(email);
    await target.locator('input[type="password"]').fill(fixture.password);
    await target.getByRole('button', { name: '登录', exact: true }).click();
    await expect(target).toHaveURL(/\/dashboard$/);
  }

  await login(page, fixture.emails.admin);
  await page.getByRole('link', { name: '节点管理', exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/nodes$/);
  await expect(page.getByRole('heading', { name: '节点与流量倍率' })).toBeVisible();
  expect(await page.title()).not.toBe('');
  await expect(
    page.getByText('尚未配置节点。导入后，符合套餐权益的客户端才会获得线路。'),
  ).toBeVisible();

  await page.getByRole('button', { name: '导入节点 YAML' }).click();
  await page.getByLabel('节点 YAML').fill(fixture.node_yaml);
  await page.getByRole('button', { name: '保存节点', exact: true }).click();
  await expect(page.getByRole('cell', { name: '0.5×', exact: true })).toBeVisible();

  await page.getByRole('button', { name: '导入节点 YAML' }).click();
  await page
    .getByLabel('节点 YAML')
    .fill(fixture.node_yaml.replace('Catalog-Half', 'Catalog-Premium'));
  await page.getByRole('textbox', { name: '线路类型', exact: true }).click();
  await page.getByRole('option', { name: '专线（需专线权益）', exact: true }).click();
  await page.getByRole('button', { name: '保存节点', exact: true }).click();
  await expect(page.getByRole('cell', { name: '1×', exact: true })).toBeVisible();

  await page.getByRole('button', { name: '编辑 Catalog-Half', exact: true }).click();
  await expect(page.getByLabel('节点 YAML')).toHaveValue(/Catalog-Half/);
  await page
    .getByLabel('节点 YAML')
    .fill(fixture.node_yaml.replace('Catalog-Half', 'Catalog-Half-Edited'));
  await page.getByRole('button', { name: '保存节点', exact: true }).click();
  await expect(
    page.getByRole('button', { name: '编辑 Catalog-Half-Edited', exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole('button', { name: '编辑 Catalog-Half-Edited', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: '编辑 Catalog-Premium', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Application error: a client-side exception has occurred'),
  ).toHaveCount(0);
  await page.screenshot({ path: path.join(output, 'admin-desktop.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('heading', { name: '节点与流量倍率' })).toBeVisible();
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1),
  ).toBe(true);
  await page.screenshot({ path: path.join(output, 'admin-mobile.png'), fullPage: true });

  const ordinaryContext = await browser.newContext({ baseURL: process.env.PLAYWRIGHT_BASE_URL });
  try {
    const ordinary = await ordinaryContext.newPage();
    ordinary.on('pageerror', (error) => errors.push(error.message));
    await login(ordinary, fixture.emails.vip);
    await ordinary.goto('/admin/nodes');
    await expect(ordinary.getByText('此页面仅对管理员开放。')).toBeVisible();
    expect((await ordinary.request.get('/api/v1/admin/nodes')).status()).toBe(403);
    await expect(ordinary.getByRole('button', { name: '导入节点 YAML' })).toHaveCount(0);
    await ordinary.screenshot({ path: path.join(output, 'ordinary-denied.png'), fullPage: true });
  } finally {
    await ordinaryContext.close();
  }
  expect(errors).toEqual([]);
});
