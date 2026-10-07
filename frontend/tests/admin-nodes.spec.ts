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

  let releaseDetail!: () => void;
  let detailStarted!: () => void;
  let detailFinished!: () => void;
  const detailGate = new Promise<void>((resolve) => {
    releaseDetail = resolve;
  });
  const detailReady = new Promise<void>((resolve) => {
    detailStarted = resolve;
  });
  const detailDone = new Promise<void>((resolve) => {
    detailFinished = resolve;
  });
  const detailRoute = '**/api/v1/admin/nodes/*';
  await page.route(detailRoute, async (route) => {
    try {
      const response = await route.fetch();
      detailStarted();
      await detailGate;
      await route.fulfill({ response });
    } finally {
      detailFinished();
    }
  });
  try {
    await page.getByRole('button', { name: '编辑 Catalog-Half', exact: true }).click();
    await detailReady;
    await page.getByRole('button', { name: '导入节点 YAML' }).click();
    const draft = fixture.node_yaml.replace('Catalog-Half', 'Unsubmitted-Draft');
    await page.getByLabel('节点 YAML').fill(draft);
    releaseDetail();
    await detailDone;
    await expect(page.getByRole('dialog')).toHaveAccessibleName('批量导入节点');
    await expect(page.getByLabel('节点 YAML')).toHaveValue(draft);
    await page.getByRole('button', { name: '取消', exact: true }).click();
  } finally {
    releaseDetail();
    await page.unroute(detailRoute);
  }

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

test('administrator imports and reopens VLESS Reality and WireGuard without losing nested options', async ({
  page,
}) => {
  const fixturePath = process.env.CATALOG_FIXTURE;
  test.skip(!fixturePath, 'Requires the private catalog acceptance runner');
  if (!fixturePath?.startsWith('/tmp/vpn-catalog-')) throw new Error('Private fixture required');
  const fixture = JSON.parse(await readFile(fixturePath, 'utf8')) as {
    emails: { admin: string };
    password: string;
  };
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(fixture.emails.admin);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto('/admin/nodes');
  const fixtures = [
    {
      name: 'VLESS-Reality-UI',
      yaml: "proxies: [{name: VLESS-Reality-UI, type: vless, server: 127.0.0.1, port: 9, uuid: 11111111-2222-4333-8444-555555555555, tls: true, network: tcp, flow: xtls-rprx-vision, servername: example.invalid, client-fingerprint: chrome, reality-opts: {public-key: ERERERERERERERERERERERERERERERERERERERERERE, short-id: '0123456789abcdef'}}]",
      preserved: 'reality-opts:',
    },
    {
      name: 'WireGuard-UI',
      yaml: 'proxies: [{name: WireGuard-UI, type: wireguard, server: 127.0.0.1, port: 9, ip: 10.0.0.2, private-key: IiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiI=, public-key: ERERERERERERERERERERERERERERERERERERERERERE=, reserved: [0, 1, 255], udp: true}]',
      preserved: 'reserved:',
    },
  ];
  for (const fixture of fixtures) {
    await page.getByRole('button', { name: '导入节点 YAML' }).click();
    await expect(page.getByText(/支持：.*vless.*wireguard/)).toBeVisible();
    await page.getByLabel('节点 YAML').fill(fixture.yaml);
    // Parse-only fixtures must not enter the existing live traffic catalog.
    await page.getByLabel('启用节点', { exact: true }).uncheck();
    await page.getByRole('button', { name: '保存节点', exact: true }).click();
    await expect(page.getByRole('dialog')).not.toBeVisible();
    await page.reload();
    await page.getByRole('button', { name: `编辑 ${fixture.name}`, exact: true }).click();
    await expect(page.getByLabel('节点 YAML')).toHaveValue(new RegExp(fixture.preserved));
    await expect(page.getByLabel('启用节点', { exact: true })).not.toBeChecked();
    await page.getByRole('button', { name: '取消', exact: true }).click();
    await page.getByRole('button', { name: `删除 ${fixture.name}`, exact: true }).click();
    await expect(page.getByRole('dialog', { name: '确认删除节点' })).toBeVisible();
    await expect(page.getByText(/历史流量及扣费记录会保留/)).toBeVisible();
    await page.getByRole('button', { name: '取消', exact: true }).click();
    await expect(page.getByRole('button', { name: `删除 ${fixture.name}`, exact: true })).toBeVisible();
    await page.getByRole('button', { name: `删除 ${fixture.name}`, exact: true }).click();
    let rejectOnce = true;
    const deleteRoute = '**/api/v1/admin/nodes/*';
    await page.route(deleteRoute, async (route) => {
      if (route.request().method() === 'DELETE' && rejectOnce) {
        rejectOnce = false;
        await route.fulfill({ status: 409, json: { error: { code: 'node_version_conflict', message: '节点已被修改，请刷新目录' } } });
      } else await route.continue();
    });
    await page.getByRole('button', { name: '确认删除', exact: true }).click();
    await expect(page.getByText('节点已被修改，请刷新目录')).toBeVisible();
    await expect(page.getByRole('dialog', { name: '确认删除节点' })).toBeVisible();
    await page.getByRole('button', { name: '确认删除', exact: true }).click();
    await expect(page.getByRole('dialog')).not.toBeVisible();
    await page.unroute(deleteRoute);
    await expect(page.getByRole('button', { name: `删除 ${fixture.name}`, exact: true })).toHaveCount(0);
    await page.reload();
    await expect(page.getByRole('button', { name: `删除 ${fixture.name}`, exact: true })).toHaveCount(0);
  }
});
