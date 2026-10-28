import { expect, request as playwrightRequest, test, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';
import { overflowing } from './layout';
import { chargedBytes, formatBytes, intervalLabel } from '../src/lib/dashboard';
import type { UsageHistory } from '../src/lib/types';
import { crispLoadFailure } from './crisp-hosts';

// Round 4 (handoff_node_use_history_and_load_balancing_c.md 12): an administrator builds the
// JP1 group for 轻量计划 from five same-named servers; simulated devices connect through the
// native API; the customer's dashboard and the administrator's counts follow the real leases.

type Fixture = {
  api: string;
  control: string;
  emails: { admin: string; lite: string };
  password: string;
  node_yaml: string;
};
type Admission = { connection: { id: string; group_id: string }; node_id: string };
type AdminGroups = {
  groups: {
    id: string;
    name: string;
    version: number;
    servers: { id: string; label: string; version: number }[];
  }[];
};

const timezoneId = 'Asia/Shanghai';

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

test('dashboard presence, device cap and node group load follow real connections', async ({
  browser,
}) => {
  test.setTimeout(240000);
  const filename = process.env.CATALOG_FIXTURE;
  const output = process.env.CATALOG_OUTPUTS;
  test.skip(!filename || !output, 'Requires the private catalog acceptance runner');
  if (!filename?.startsWith('/tmp/vpn-catalog-') || !output)
    throw new Error('Private fixture required');
  const fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
  await mkdir(output, { recursive: true });
  const origin = process.env.PLAYWRIGHT_BASE_URL!;
  const errors: string[] = [];
  const adminContext = await browser.newContext({ baseURL: origin, timezoneId });
  const liteContext = await browser.newContext({ baseURL: origin, timezoneId });
  const native = await playwrightRequest.newContext({ baseURL: fixture.api });
  const admin = await adminContext.newPage();
  const lite = await liteContext.newPage();
  for (const page of [admin, lite]) page.on('pageerror', (error) => errors.push(error.message));
  const groupYAML = fixture.node_yaml.replace('Catalog-Half', 'JP1');
  try {
    // Administrator: 轻量计划 → new group JP1 → five same-named servers, capacity 5 each.
    await login(admin, fixture.emails.admin, fixture.password);
    await admin.goto('/admin/nodes');
    await expect(admin.getByRole('heading', { name: '节点组与服务器' })).toBeVisible();
    await admin.getByRole('textbox', { name: '套餐筛选', exact: true }).click();
    await admin.getByRole('option', { name: '轻量计划', exact: true }).click();
    await admin.getByRole('button', { name: '新建节点组', exact: true }).click();
    const groupDialog = admin.getByRole('dialog', { name: '新建节点组' });
    // Creating under a plan filter starts scoped to that plan.
    await expect(groupDialog.getByText('轻量计划', { exact: true })).toBeVisible();
    await groupDialog.getByLabel('组名').fill('JP1');
    await groupDialog.getByLabel('地区').fill('JP');
    await groupDialog.getByRole('button', { name: '保存节点组', exact: true }).click();
    const card = admin.getByRole('region', { name: '节点组 JP1', exact: true });
    await expect(card).toBeVisible();
    await expect(card.getByText('可用套餐：轻量计划')).toBeVisible();
    for (let index = 1; index <= 5; index++) {
      await card.getByRole('button', { name: '导入服务器到 JP1', exact: true }).click();
      const dialog = admin.getByRole('dialog', { name: '导入服务器' });
      await dialog.getByLabel('服务器 YAML').fill(groupYAML);
      await expect(dialog.getByLabel('最大在线设备数')).toHaveValue('5');
      await dialog.getByLabel('备注').fill(`jp-${index}`);
      await dialog.getByRole('button', { name: '保存服务器', exact: true }).click();
      await expect(dialog).not.toBeVisible();
    }
    await expect(card.getByRole('row', { name: /^服务器 jp-\d$/ })).toHaveCount(5);
    for (let index = 1; index <= 5; index++)
      await expect(
        card.getByRole('row', { name: `服务器 jp-${index}`, exact: true }).getByRole('cell', {
          name: 'JP1',
          exact: true,
        }),
      ).toBeVisible();
    await expect(card.getByText('在线 0 / 容量 25', { exact: true })).toBeVisible();
    await expect(card.getByText('5 台服务器', { exact: true })).toBeVisible();
    await admin.screenshot({ path: path.join(output, 'admin-groups-desktop.png'), fullPage: true });
    const listed = (await (
      await admin.request.get('/api/v1/admin/node-groups')
    ).json()) as AdminGroups;
    const jp1 = listed.groups.find((group) => group.name === 'JP1')!;
    const serverIDs = jp1.servers.map((server) => server.id);
    expect(serverIDs).toHaveLength(5);

    // Customer dashboard before any connection.
    await login(lite, fixture.emails.lite, fixture.password);
    lite.on('console', (entry) => {
      if (entry.type() === 'error' && !crispLoadFailure(entry)) errors.push(entry.text());
    });
    const devicesCard = lite.getByRole('region', { name: '连接与客户端' });
    await expect(devicesCard.getByText('在线设备 0 / 2', { exact: true })).toBeVisible();
    await expect(devicesCard.getByText('当前没有在线设备')).toBeVisible();

    // Three simulated client devices sign in; signing in alone takes no slot.
    const devices: { token: string; platform: string }[] = [];
    for (const platform of ['macos', 'windows', 'android']) {
      const response = await native.post('/api/v1/client/login', {
        data: {
          email: fixture.emails.lite,
          password: fixture.password,
          device_id: randomUUID(),
          platform,
          app_version: `round4-${platform}`,
        },
      });
      expect(response.status()).toBe(200);
      devices.push({ token: (await response.json()).token, platform });
    }
    const connect = (token: string) =>
      native.post('/api/v1/client/connections', {
        data: { group_id: jp1.id, last_sequence: 0 },
        headers: { Authorization: `Bearer ${token}`, 'Idempotency-Key': randomUUID() },
      });
    const admissions: Admission[] = [];
    for (const device of devices.slice(0, 2)) {
      const response = await connect(device.token);
      expect(response.status()).toBe(201);
      const admitted = (await response.json()) as Admission;
      expect(serverIDs).toContain(admitted.node_id);
      expect(admitted.connection.group_id).toBe(jp1.id);
      admissions.push(admitted);
    }
    // Sticky assignment: the user's second device returns to the user's server.
    expect(admissions[1].node_id).toBe(admissions[0].node_id);
    const third = await connect(devices[2].token);
    expect(third.status()).toBe(403);
    expect((await third.json()).error.code).toBe('device_limit');

    // Within one dashboard refresh (15 s) the customer sees 2 / 2 and both devices.
    await expect(devicesCard.getByText('在线设备 2 / 2', { exact: true })).toBeVisible({
      timeout: 20000,
    });
    await expect(devicesCard.getByRole('listitem', { name: /^在线设备 / })).toHaveCount(2);
    await expect(devicesCard.getByText('JP1', { exact: true })).toHaveCount(2);
    await expect(devicesCard.getByText(/^macOS round4-macos · 设备 …[0-9a-f]{6}$/)).toBeVisible();
    await expect(devicesCard.getByText('已登录未连接（不占名额）')).toBeVisible();
    await expect(devicesCard.getByText(/^Android round4-android · 设备 …/)).toBeVisible();
    await expect(card.getByText('在线 2 / 容量 25', { exact: true })).toBeVisible({
      timeout: 15000,
    });
    const devicesJSON = await (await lite.request.get('/api/v1/me/devices')).text();
    for (const id of [...serverIDs, ...admissions.map((item) => item.connection.id)])
      expect(devicesJSON).not.toContain(id);

    // One device disconnects: 1 / 2 on the dashboard, one fewer on the server.
    const released = await native.post(
      `/api/v1/client/connections/${admissions[0].connection.id}/release`,
      { data: { reason: 'user' }, headers: { Authorization: `Bearer ${devices[0].token}` } },
    );
    expect(released.status()).toBe(200);
    await expect(devicesCard.getByText('在线设备 1 / 2', { exact: true })).toBeVisible({
      timeout: 20000,
    });
    await expect(card.getByText('在线 1 / 容量 25', { exact: true })).toBeVisible({
      timeout: 15000,
    });

    // Disabling the server of the remaining device moves it to another server of JP1.
    const remaining = jp1.servers.find((server) => server.id === admissions[1].node_id)!;
    const row = card.getByRole('row', { name: `服务器 ${remaining.label}`, exact: true });
    await expect(row.getByText('1 / 5', { exact: true })).toBeVisible();
    await row.getByRole('button', { name: `编辑服务器 ${remaining.label}`, exact: true }).click();
    const edit = admin.getByRole('dialog', { name: '编辑服务器' });
    await expect(edit.getByLabel('服务器 YAML')).toHaveValue(/JP1/);
    await edit.getByLabel('启用服务器').uncheck();
    await edit.getByRole('button', { name: '保存服务器', exact: true }).click();
    await expect(edit).not.toBeVisible();
    await expect(row.getByText('已停用', { exact: true })).toBeVisible();
    await expect(row.getByText('0 / 5', { exact: true })).toBeVisible();
    await expect(card.getByText('在线 0 / 容量 20', { exact: true })).toBeVisible();
    const session = await native.get('/api/v1/client/session', {
      headers: { Authorization: `Bearer ${devices[1].token}` },
    });
    expect((await session.json()).reason).toBe('connection_reassigned');
    const moved = await connect(devices[1].token);
    expect(moved.status()).toBe(201);
    const reassigned = (await moved.json()) as Admission;
    expect(reassigned.node_id).not.toBe(remaining.id);
    expect(serverIDs).toContain(reassigned.node_id);
    await expect(card.getByText('在线 1 / 容量 20', { exact: true })).toBeVisible({
      timeout: 15000,
    });
    const target = jp1.servers.find((server) => server.id === reassigned.node_id)!;
    await expect(
      card
        .getByRole('row', { name: `服务器 ${target.label}`, exact: true })
        .getByText('1 / 5', { exact: true }),
    ).toBeVisible();
    await expect(devicesCard.getByText('在线设备 1 / 2', { exact: true })).toBeVisible();
    await lite.screenshot({ path: path.join(output, 'dashboard-online.png'), fullPage: true });

    // 24-hour chart: fixed history across 26 hours; the page shows the API's 8 intervals.
    const seeded = await native.post(`/fixture/usage-history`, {
      headers: { 'X-Acceptance-Control': fixture.control },
    });
    expect(seeded.status()).toBe(204);
    await lite.reload();
    const history = (await (
      await lite.request.get('/api/v1/me/usage-history')
    ).json()) as UsageHistory;
    expect(history.buckets).toHaveLength(8);
    expect(history.bucket_seconds).toBe(10800);
    const mib = 1024 ** 2;
    // The row 26 hours ago is outside the window.
    expect(history.total_upload_bytes).toBe((3 + 40 + 1 + 300 + 0 + 2 + 5) * mib);
    expect(history.total_download_bytes).toBe((12 + 200 + 0 + 1500 + 64 + 8 + 25) * mib);
    const bars = lite.getByRole('group', { name: /^近 24 小时流量柱状图/ }).getByRole('button');
    await expect(bars).toHaveCount(8);
    const labels = history.buckets.map((interval, index) => {
      const newest = index === history.buckets.length - 1;
      return `${intervalLabel(interval, newest, timezoneId).text}：上传 ${formatBytes(
        interval.upload_bytes,
      )}，下载 ${formatBytes(interval.download_bytes)}，合计 ${formatBytes(
        interval.upload_bytes + interval.download_bytes,
      )}，计费 ${formatBytes(chargedBytes(interval.charged_units))}`;
    });
    for (let index = 0; index < 8; index++)
      await expect(bars.nth(index)).toHaveAccessibleName(labels[index]);
    expect(labels[7]).toContain('至现在');
    // Keyboard: every bar is reachable with Tab and announces its values.
    await bars.first().focus();
    for (let index = 0; index < 8; index++) {
      await expect(bars.nth(index)).toBeFocused();
      await expect(lite.getByText(labels[index], { exact: true })).toBeVisible();
      if (index < 7) await lite.keyboard.press('Tab');
    }
    await lite.getByText('以表格查看', { exact: true }).click();
    await expect(
      lite.getByRole('cell', { name: intervalLabel(history.buckets[7], true, timezoneId).text }),
    ).toBeVisible();
    await lite.screenshot({
      path: path.join(output, 'dashboard-history-desktop.png'),
      fullPage: true,
    });
    await lite.setViewportSize({ width: 390, height: 844 });
    await expect(bars).toHaveCount(8);
    await expect.poll(() => overflowing(lite)).toEqual([]);
    await lite.screenshot({ path: path.join(output, 'dashboard-mobile.png'), fullPage: true });

    // Neither the page nor browser storage carries YAML, addresses or server ids.
    const exposed = await lite.evaluate(() =>
      JSON.stringify({
        html: document.documentElement.outerHTML,
        local: { ...localStorage },
        session: { ...sessionStorage },
      }),
    );
    const port = /port: (\d+)/.exec(fixture.node_yaml)![1];
    for (const secret of [...serverIDs, 'proxies', 'socks5', `port: ${port}`, `"${port}"`])
      expect(exposed).not.toContain(secret);
    expect(errors).toEqual([]);
  } finally {
    // Leave no JP1 leases, servers or group behind for the later catalog checks.
    const current = (await (
      await admin.request.get('/api/v1/admin/node-groups')
    ).json()) as AdminGroups;
    const group = current.groups.find((item) => item.name === 'JP1');
    for (const server of group?.servers ?? []) {
      const removed = await admin.request.delete(`/api/v1/admin/nodes/${server.id}`, {
        data: { version: server.version },
        headers: { Origin: origin },
      });
      expect(removed.status()).toBe(200);
    }
    if (group) {
      const removed = await admin.request.delete(`/api/v1/admin/node-groups/${group.id}`, {
        data: { version: group.version },
        headers: { Origin: origin },
      });
      expect(removed.status()).toBe(200);
    }
    await native.dispose();
    await adminContext.close();
    await liteContext.close();
  }
});
