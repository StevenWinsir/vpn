import { expect, test, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { overflowing } from './layout';

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
  await expect(page.getByRole('heading', { name: '节点组与服务器' })).toBeVisible();
  expect(await page.title()).not.toBe('');
  await expect(
    page.getByText('尚未配置节点组。先新建节点组，再向组内导入服务器 YAML。'),
  ).toBeVisible();

  // A line is a node group; its servers carry the YAML. The legacy Mihomo acceptance that
  // follows connects to the groups "Catalog-Half-Edited" (0.5x) and "Catalog-Premium" (1x).
  async function createGroup(name: string, dedicated = false) {
    await page.getByRole('button', { name: '新建节点组', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '新建节点组' });
    await dialog.getByLabel('组名').fill(name);
    if (dedicated) {
      await dialog.getByRole('textbox', { name: '线路类型', exact: true }).click();
      await page.getByRole('option', { name: '专线（需专线权益）', exact: true }).click();
    }
    await dialog.getByRole('button', { name: '保存节点组', exact: true }).click();
    await expect(dialog).not.toBeVisible();
    return page.getByRole('region', { name: `节点组 ${name}`, exact: true });
  }
  async function importServer(group: string, yaml: string, label: string) {
    await page.getByRole('button', { name: `导入服务器到 ${group}`, exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '导入服务器' });
    await dialog.getByLabel('服务器 YAML').fill(yaml);
    await dialog.getByLabel('备注').fill(label);
    await dialog.getByRole('button', { name: '保存服务器', exact: true }).click();
    await expect(dialog).not.toBeVisible();
  }
  const half = await createGroup('Catalog-Half');
  await expect(half.getByText(/倍率 0\.5×/)).toBeVisible();
  await expect(half.getByText(/组内还没有服务器/)).toBeVisible();
  await importServer('Catalog-Half', fixture.node_yaml, 'half-a');
  await expect(half.getByRole('row', { name: '服务器 half-a', exact: true })).toBeVisible();
  await expect(half.getByText('在线 0 / 容量 5', { exact: true })).toBeVisible();
  const premium = await createGroup('Catalog-Premium', true);
  await expect(premium.getByText(/专线 · 倍率 1×/)).toBeVisible();
  await importServer(
    'Catalog-Premium',
    fixture.node_yaml.replace('Catalog-Half', 'Catalog-Premium'),
    'premium-a',
  );
  await expect(premium.getByRole('row', { name: '服务器 premium-a', exact: true })).toBeVisible();

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
    // A late server detail must not replace a draft the administrator started meanwhile.
    await page.getByRole('button', { name: '编辑服务器 half-a', exact: true }).click();
    await detailReady;
    await page.getByRole('button', { name: '导入服务器到 Catalog-Half', exact: true }).click();
    const draft = fixture.node_yaml.replace('Catalog-Half', 'Unsubmitted-Draft');
    await page.getByLabel('服务器 YAML').fill(draft);
    releaseDetail();
    await detailDone;
    await expect(page.getByRole('dialog')).toHaveAccessibleName('导入服务器');
    await expect(page.getByLabel('服务器 YAML')).toHaveValue(draft);
    await page.getByRole('button', { name: '取消', exact: true }).click();
  } finally {
    releaseDetail();
    await page.unroute(detailRoute);
  }

  await half.getByRole('button', { name: '编辑节点组 Catalog-Half', exact: true }).click();
  const groupEdit = page.getByRole('dialog', { name: '编辑节点组' });
  await expect(groupEdit.getByLabel('组名')).toHaveValue('Catalog-Half');
  await groupEdit.getByLabel('组名').fill('Catalog-Half-Edited');
  await groupEdit.getByRole('button', { name: '保存节点组', exact: true }).click();
  await expect(groupEdit).not.toBeVisible();
  const edited = page.getByRole('region', { name: '节点组 Catalog-Half-Edited', exact: true });
  await expect(edited.getByText(/版本 2$/)).toBeVisible();
  await edited.getByRole('button', { name: '编辑服务器 half-a', exact: true }).click();
  const serverEdit = page.getByRole('dialog', { name: '编辑服务器' });
  await expect(serverEdit.getByLabel('服务器 YAML')).toHaveValue(/Catalog-Half/);
  await serverEdit.getByLabel('最大在线设备数').fill('8');
  await serverEdit.getByRole('button', { name: '保存服务器', exact: true }).click();
  await expect(serverEdit).not.toBeVisible();
  // A capacity edit does not move the server version.
  const halfRow = edited.getByRole('row', { name: '服务器 half-a', exact: true });
  await expect(halfRow.getByText('0 / 8', { exact: true })).toBeVisible();
  await expect(halfRow.getByRole('cell', { name: '1', exact: true })).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole('region', { name: '节点组 Catalog-Half-Edited', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('region', { name: '节点组 Catalog-Premium', exact: true }),
  ).toBeVisible();
  // Unscoped groups are listed under the "不限套餐" filter.
  await page.getByRole('textbox', { name: '套餐筛选', exact: true }).click();
  await page.getByRole('option', { name: '不限套餐', exact: true }).click();
  await expect(page.getByRole('region', { name: /^节点组 Catalog-/ })).toHaveCount(2);
  await expect(
    page.getByText('Application error: a client-side exception has occurred'),
  ).toHaveCount(0);
  await page.screenshot({ path: path.join(output, 'admin-desktop.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('heading', { name: '节点组与服务器' })).toBeVisible();
  await expect.poll(() => overflowing(page)).toEqual([]);
  await page.screenshot({ path: path.join(output, 'admin-mobile.png'), fullPage: true });

  const ordinaryContext = await browser.newContext({ baseURL: process.env.PLAYWRIGHT_BASE_URL });
  try {
    const ordinary = await ordinaryContext.newPage();
    ordinary.on('pageerror', (error) => errors.push(error.message));
    await login(ordinary, fixture.emails.vip);
    await ordinary.goto('/admin/nodes');
    await expect(ordinary.getByText('此页面仅对管理员开放。')).toBeVisible();
    expect((await ordinary.request.get('/api/v1/admin/nodes')).status()).toBe(403);
    await expect(ordinary.getByRole('button', { name: '新建节点组' })).toHaveCount(0);
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
  // Parse-only fixtures live in a disabled group with disabled servers, so they never enter
  // the live traffic catalog.
  await page.getByRole('button', { name: '新建节点组', exact: true }).click();
  const groupDialog = page.getByRole('dialog', { name: '新建节点组' });
  await groupDialog.getByLabel('组名').fill('Protocol-Fixtures');
  await groupDialog.getByLabel('启用节点组', { exact: true }).uncheck();
  await groupDialog.getByRole('button', { name: '保存节点组', exact: true }).click();
  const group = page.getByRole('region', { name: '节点组 Protocol-Fixtures', exact: true });
  await expect(group.getByText('已停用', { exact: true })).toBeVisible();
  for (const fixture of fixtures) {
    await group
      .getByRole('button', { name: '导入服务器到 Protocol-Fixtures', exact: true })
      .click();
    await expect(page.getByText(/支持：.*vless.*wireguard/)).toBeVisible();
    await page.getByLabel('服务器 YAML').fill(fixture.yaml);
    await page.getByLabel('启用服务器', { exact: true }).uncheck();
    await page.getByRole('button', { name: '保存服务器', exact: true }).click();
    await expect(page.getByRole('dialog')).not.toBeVisible();
    await page.reload();
    await page.getByRole('button', { name: `编辑服务器 ${fixture.name}`, exact: true }).click();
    await expect(page.getByLabel('服务器 YAML')).toHaveValue(new RegExp(fixture.preserved));
    await expect(page.getByLabel('启用服务器', { exact: true })).not.toBeChecked();
    await page.getByRole('button', { name: '取消', exact: true }).click();
    await page.getByRole('button', { name: `删除服务器 ${fixture.name}`, exact: true }).click();
    await expect(page.getByRole('dialog', { name: '确认删除服务器' })).toBeVisible();
    await expect(page.getByText(/当前 0 台在线设备会自动改连同组其他服务器/)).toBeVisible();
    await expect(page.getByText(/历史流量及扣费记录会保留/)).toBeVisible();
    await page.getByRole('button', { name: '取消', exact: true }).click();
    await page.getByRole('button', { name: `删除服务器 ${fixture.name}`, exact: true }).click();
    let rejectOnce = true;
    const deleteRoute = '**/api/v1/admin/nodes/*';
    await page.route(deleteRoute, async (route) => {
      if (route.request().method() === 'DELETE' && rejectOnce) {
        rejectOnce = false;
        await route.fulfill({
          status: 409,
          json: { error: { code: 'node_version_conflict', message: '节点已被修改，请刷新目录' } },
        });
      } else await route.continue();
    });
    await page.getByRole('button', { name: '确认删除', exact: true }).click();
    await expect(page.getByText('节点已被修改，请刷新目录')).toBeVisible();
    await expect(page.getByRole('dialog', { name: '确认删除服务器' })).toBeVisible();
    await page.getByRole('button', { name: '确认删除', exact: true }).click();
    await expect(page.getByRole('dialog')).not.toBeVisible();
    await page.unroute(deleteRoute);
    await expect(
      page.getByRole('button', { name: `删除服务器 ${fixture.name}`, exact: true }),
    ).toHaveCount(0);
    await page.reload();
    await expect(
      page.getByRole('button', { name: `删除服务器 ${fixture.name}`, exact: true }),
    ).toHaveCount(0);
  }
  // Only an empty group can be deleted.
  await group.getByRole('button', { name: '删除节点组 Protocol-Fixtures', exact: true }).click();
  await expect(page.getByRole('dialog', { name: '确认删除节点组' })).toBeVisible();
  await page.getByRole('button', { name: '确认删除', exact: true }).click();
  await expect(page.getByRole('dialog')).not.toBeVisible();
  await expect(group).toHaveCount(0);
});
