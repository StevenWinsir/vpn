import { expect, test, type Page } from '@playwright/test';
import { readFile, mkdir } from 'node:fs/promises';
import path from 'node:path';
import type { NodeCatalog, PublicNode } from '../src/lib/types';

type Fixture = { emails: { admin: string; vip: string }; password: string; node_yaml: string };
const sample: PublicNode = {
  id: 'sample',
  name: '香港 HK-01',
  region: 'HK',
  line_type: 'dedicated',
  rate_permille: 1000,
  version: 1,
};
const catalog = (nodes = [sample]): NodeCatalog => ({
  nodes,
  entitlement_active: true,
  expires_at: '2099-01-01T00:00:00Z',
  is_test: false,
});

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

async function mockUser(page: Page) {
  await page.route('**/api/v1/auth/me', (route) =>
    route.fulfill({
      json: {
        user: {
          id: 'node-reader',
          name: '节点测试',
          email: 'nodes@example.invalid',
          role: 'user',
          created_at: '2025-01-01T00:00:00Z',
        },
      },
    }),
  );
}

test('web catalog reflects real administrator changes without exposing connection parameters', async ({
  page,
  browser,
}) => {
  const filename = process.env.CATALOG_FIXTURE;
  const output = process.env.CATALOG_OUTPUTS;
  test.skip(!filename || !output, 'Requires the private catalog acceptance runner');
  if (!filename?.startsWith('/tmp/vpn-catalog-') || !output)
    throw new Error('Private fixture required');
  const fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
  const adminContext = await browser.newContext({ baseURL: process.env.PLAYWRIGHT_BASE_URL });
  const admin = await adminContext.newPage();
  const created = new Map<string, number>();
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const origin = process.env.PLAYWRIGHT_BASE_URL!;
  const body = (name: string, line = 'direct', enabled = true, plans: string[] = []) => ({
    yaml: fixture.node_yaml.replace('Catalog-Half', name),
    region: 'HK',
    line_type: line,
    rate_permille: line === 'direct' ? 500 : 1000,
    enabled,
    plan_ids: plans,
  });
  const save = async (input: ReturnType<typeof body> & { version?: number }, id = '') => {
    const response = await admin.request.post(`/api/v1/admin/nodes${id ? `/${id}` : ''}`, {
      data: input,
      headers: { Origin: origin },
    });
    expect(response.status()).toBe(id ? 200 : 201);
    const node = (await response.json()).nodes[0] as PublicNode;
    created.set(node.id, node.version);
    return node;
  };
  try {
    await login(admin, fixture.emails.admin, fixture.password);
    const direct = await save(body('UserList-普通'));
    const premium = await save(body('UserList-专线', 'dedicated'));
    await save(body('UserList-停用', 'direct', false));
    await save(body('UserList-其他套餐', 'direct', true, ['max']));
    await page.clock.install();
    await login(page, fixture.emails.vip, fixture.password);
    page.on('console', (entry) => {
      if (entry.type() === 'error' || entry.type() === 'warning') errors.push(entry.text());
    });
    const sensitiveRequests: string[] = [];
    page.on('request', (request) => {
      const pathname = new URL(request.url()).pathname;
      if (/\/api\/v1\/(admin\/|client\/(config|traffic))/.test(pathname))
        sensitiveRequests.push(pathname);
    });
    const responsePromise = page.waitForResponse(
      (response) =>
        response.url().endsWith('/api/v1/client/bootstrap') && response.status() === 200,
    );
    await page.getByRole('link', { name: '节点列表', exact: true }).click();
    const response = await responsePromise;
    expect(response.headers()['cache-control']).toContain('no-store');
    const payload = await response.json();
    for (const node of payload.nodes) {
      expect(Object.keys(node).sort()).toEqual([
        'id',
        'line_type',
        'name',
        'rate_permille',
        'region',
        'version',
      ]);
    }
    expect(JSON.stringify(payload)).not.toMatch(
      /127\.0\.0\.1|proxies:|ciphertext|"yaml"|"password"|"server"|"port"/,
    );
    await expect(page).toHaveURL(/\/nodes$/);
    await expect(page).toHaveTitle(/^节点列表 · /);
    await expect(page.getByRole('heading', { name: '节点列表', exact: true })).toBeVisible();
    await page.getByLabel('搜索节点').fill('UserList-');
    await expect(page.getByRole('article')).toHaveCount(2);
    await expect(
      page
        .getByRole('article', { name: '节点 UserList-普通', exact: true })
        .getByText('0.5×', { exact: true }),
    ).toBeVisible();
    await expect(
      page
        .getByRole('article', { name: '节点 UserList-专线', exact: true })
        .getByText('1×', { exact: true }),
    ).toBeVisible();
    await expect(page.getByText('UserList-停用', { exact: true })).toHaveCount(0);
    await expect(page.getByText('UserList-其他套餐', { exact: true })).toHaveCount(0);
    await expect(page.getByRole('link', { name: '节点管理', exact: true })).toHaveCount(0);
    await mkdir(output, { recursive: true });
    await page.screenshot({ path: path.join(output, 'nodes-desktop.png'), fullPage: true });

    await page.getByRole('textbox', { name: '线路类型', exact: true }).click();
    await page.getByRole('option', { name: '专线', exact: true }).click();
    await expect(page.getByRole('article')).toHaveCount(1);
    await page.getByLabel('搜索节点').fill('no-such-node');
    await expect(page.getByRole('heading', { name: '没有匹配的节点' })).toBeVisible();
    await page.getByRole('button', { name: '清除筛选', exact: true }).click();
    await page.getByLabel('搜索节点').fill('UserList-');

    await save(
      { ...body('UserList-更新'), region: 'JP', rate_permille: 1250, version: direct.version },
      direct.id,
    );
    await page.clock.fastForward(30000);
    await expect(
      page
        .getByRole('article', { name: '节点 UserList-更新', exact: true })
        .getByText('1.25×', { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole('article', { name: '节点 UserList-普通', exact: true }),
    ).toHaveCount(0);
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByRole('main')).toHaveCSS('padding-left', '0px');
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1),
    ).toBe(true);
    await page.screenshot({ path: path.join(output, 'nodes-mobile.png'), fullPage: true });
    await save(
      { ...body('UserList-更新', 'direct', false), version: created.get(direct.id) },
      direct.id,
    );
    await page.getByRole('button', { name: '同步节点', exact: true }).click();
    await expect(
      page.getByRole('article', { name: '节点 UserList-更新', exact: true }),
    ).toHaveCount(0);
    const removed = await admin.request.delete(`/api/v1/admin/nodes/${premium.id}`, {
      data: { version: created.get(premium.id) },
      headers: { Origin: origin },
    });
    expect(removed.status()).toBe(200);
    created.delete(premium.id);
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    await expect(page.getByRole('article')).toHaveCount(0);
    await expect(page.getByRole('heading', { name: '没有匹配的节点' })).toBeVisible();
    expect(sensitiveRequests).toEqual([]);
    expect(errors).toEqual([]);
  } finally {
    for (const [id, version] of created) {
      const response = await admin.request.delete(`/api/v1/admin/nodes/${id}`, {
        data: { version },
        headers: { Origin: origin },
      });
      expect(response.status()).toBe(200);
    }
    await adminContext.close();
  }
});

test('refresh failure, inactive entitlement, empty catalog and expired login clear stale nodes', async ({
  page,
}) => {
  await mockUser(page);
  let reply: { status: number; json: unknown } = { status: 200, json: catalog() };
  await page.route('**/api/v1/client/bootstrap', (route) => route.fulfill(reply));
  await page.route('**/api/v1/auth/refresh', (route) =>
    route.fulfill({
      status: 401,
      json: { error: { code: 'unauthorized', message: '请重新登录' } },
    }),
  );
  await page.goto('/nodes');
  await expect(page.getByRole('article')).toHaveCount(1);
  reply = {
    status: 503,
    json: { error: { code: 'database_unavailable', message: '节点信息暂时不可用' } },
  };
  await page.getByRole('button', { name: '同步节点', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: '节点同步失败' })).toBeVisible();
  await expect(page.getByRole('article')).toHaveCount(0);
  reply = {
    status: 403,
    json: { error: { code: 'entitlement_inactive', message: '套餐不存在、已过期或流量不足' } },
  };
  await page.getByRole('button', { name: '同步节点', exact: true }).click();
  await expect(page.getByRole('heading', { name: '套餐权益不可用' })).toBeVisible();
  await expect(page.getByRole('link', { name: '查看套餐', exact: true })).toHaveAttribute(
    'href',
    '/plans',
  );
  reply = { status: 200, json: catalog([]) };
  await page.getByRole('button', { name: '同步节点', exact: true }).click();
  await expect(page.getByRole('heading', { name: '暂无可见节点' })).toBeVisible();
  reply = { status: 200, json: catalog() };
  await page.getByRole('button', { name: '同步节点', exact: true }).click();
  await expect(page.getByRole('article')).toHaveCount(1);
  reply = { status: 401, json: { error: { code: 'unauthorized', message: '请重新登录' } } };
  await page.getByRole('button', { name: '同步节点', exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByRole('article')).toHaveCount(0);
});

test('polling pauses while hidden, refreshes on return and deduplicates in-flight requests', async ({
  page,
}) => {
  await mockUser(page);
  await page.clock.install();
  let requests = 0;
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/v1/client/bootstrap', async (route) => {
    requests++;
    if (requests === 1) await gate;
    await route.fulfill({ json: catalog() });
  });
  try {
    await page.goto('/nodes');
    await expect(page.getByText('正在同步节点…', { exact: true })).toBeVisible();
    await expect.poll(() => requests).toBe(1);
    await page.evaluate(() => {
      window.dispatchEvent(new Event('focus'));
      window.dispatchEvent(new Event('online'));
    });
    await page.clock.fastForward(30000);
    expect(requests).toBe(1);
    release();
    await expect(page.getByRole('article')).toHaveCount(1);
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await page.clock.fastForward(30000);
    expect(requests).toBe(1);
    await page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await expect.poll(() => requests).toBe(2);
    await expect(page.getByRole('button', { name: '同步节点', exact: true })).toBeEnabled();
    await page.goto('/login');
    const before = requests;
    await page.clock.fastForward(60000);
    expect(requests).toBe(before);
  } finally {
    release();
  }
});

test('node names are rendered as text and long labels fit mobile', async ({ page }) => {
  await mockUser(page);
  const name = '<img src=x onerror=alert(1)>' + 'Long-Node-'.repeat(9);
  await page.route('**/api/v1/client/bootstrap', (route) =>
    route.fulfill({ json: catalog([{ ...sample, name }]) }),
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/nodes');
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
  await expect(page.locator('article img')).toHaveCount(0);
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1),
  ).toBe(true);
});

test('anonymous visitors cannot open the node list', async ({ page }) => {
  await page.route('**/api/v1/auth/{me,refresh}', (route) =>
    route.fulfill({ status: 401, json: { error: { code: 'unauthorized', message: '请登录' } } }),
  );
  let catalogs = 0;
  page.on('request', (request) => {
    if (request.url().endsWith('/client/bootstrap')) catalogs++;
  });
  await page.goto('/nodes');
  await expect(page).toHaveURL(/\/login$/);
  expect(catalogs).toBe(0);
});
