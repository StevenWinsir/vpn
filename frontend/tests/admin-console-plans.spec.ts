import { expect, test, type Browser, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';

// Browser acceptance for the plan console (handoff_shop.md round 2; S2-01 … S2-07). It runs
// against the real API on a private PostgreSQL, started by scripts/test-payment-web.py, which
// every spec shares: this one creates its own e2e- plans, never edits starter/pro/max and puts
// the global yearly discount back to 90 whatever happens.
type Fixture = {
  api: string;
  password: string;
  control: string;
  emails: Record<'judy' | 'kyle' | 'boss', string>;
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

async function signIn(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

async function visitor(browser: Browser, email: string) {
  const context = await browser.newContext();
  const page = await context.newPage();
  await signIn(page, email);
  return { context, page };
}

// A same-origin call made with the signed-in page's cookies.
async function call(page: Page, path: string, method = 'GET', body?: unknown) {
  return page.evaluate(
    async ([p, m, b]) => {
      const response = await fetch('/api/v1' + p, {
        method: m as string,
        headers: b ? { 'Content-Type': 'application/json' } : undefined,
        body: b ? (b as string) : undefined,
      });
      return { status: response.status, body: await response.json().catch(() => null) };
    },
    [path, method, body === undefined ? '' : JSON.stringify(body)] as const,
  );
}

type Plans = {
  plans: { id: string; version: number; deleted_at: string | null }[];
  settings: { yearly_discount_percent: number; version: number };
  client_max_online_devices: number;
};
const plansOf = async (page: Page) =>
  (await call(page, '/admin/console/plans?include_deleted=true')).body as Plans;

const inputKeys = [
  'name',
  'description',
  'note',
  'price_cents',
  'duration_days',
  'traffic_bytes',
  'max_devices',
  'allow_dedicated',
  'yearly_enabled',
  'yearly_duration_days',
  'yearly_traffic_bytes',
  'yearly_discount_percent',
  'featured',
  'sort',
];

// The terms of a valid plan; a test overrides what it needs.
const terms = (id: string, name: string, extra: Record<string, unknown> = {}) => ({
  id,
  name,
  description: '',
  note: '',
  price_cents: 1990,
  duration_days: 30,
  traffic_bytes: 50 * 1024 ** 3,
  max_devices: 2,
  allow_dedicated: false,
  yearly_enabled: true,
  yearly_duration_days: 365,
  yearly_traffic_bytes: null,
  yearly_discount_percent: null,
  featured: false,
  sort: 900,
  active: false,
  ...extra,
});

const row = (page: Page, id: string) =>
  page.locator(`[data-testid="plan-row"][data-plan-id="${id}"]`);

test.describe.configure({ mode: 'serial' });

let admin: Page;
let adminContext: Awaited<ReturnType<Browser['newContext']>>;
test.beforeAll(async ({ browser }) => {
  ({ context: adminContext, page: admin } = await visitor(browser, fixture.emails.boss));
});

// Leave nothing behind for the specs that follow: the discount is back to 90, every e2e plan
// is off the shelf (or deleted), and no e2e group or coupon remains.
test.afterAll(async () => {
  try {
    const state = await plansOf(admin);
    if (state.settings.yearly_discount_percent !== 90)
      await call(admin, '/admin/console/shop-settings', 'POST', {
        yearly_discount_percent: 90,
        version: state.settings.version,
      });
    const views = (await call(admin, '/admin/console/plans')).body as {
      plans: Record<string, unknown>[];
    };
    for (const view of views.plans) {
      if (!String(view.id).startsWith('e2e-') || !view.active) continue;
      const input: Record<string, unknown> = { active: false, version: view.version };
      for (const key of inputKeys) input[key] = view[key];
      await call(admin, `/admin/console/plans/${view.id}`, 'POST', input);
    }
    const coupons = (await call(admin, '/admin/console/coupons')).body as {
      coupons: { id: string; code: string }[];
    };
    for (const c of coupons?.coupons ?? [])
      if (c.code.startsWith('E2E-'))
        await call(admin, `/admin/console/coupons/${c.id}`, 'DELETE', {});
  } finally {
    await adminContext.close();
  }
});

test('S2-01 create, price, enable yearly, publish and withdraw a plan; the shop follows', async ({
  browser,
}) => {
  await admin.goto('/admin/console/plans');
  await expect(admin.getByRole('link', { name: '套餐管理' })).toBeVisible();
  await expect(admin.getByTestId('discount-current')).toContainText('年付默认 9 折，省 10%');

  await admin.getByRole('button', { name: '新建套餐' }).click();
  let modal = admin.getByRole('dialog', { name: '新建套餐' });
  await modal.getByLabel('套餐 ID').fill('e2e-a');
  // HTML in a name must come out as text.
  await modal.getByLabel('名称', { exact: true }).fill('E2E <i>套餐</i> A');
  await modal.getByLabel('公开描述').fill('端到端 <script>alert(1)</script>');
  await modal.getByLabel('内部备注').fill('仅后台可见的备注');
  await modal.getByLabel('月付价格（元）').fill('12.34');
  await modal.getByLabel('月付流量（GiB）').fill('50');
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(admin.getByTestId('plan-saved')).toContainText('套餐“E2E <i>套餐</i> A”已保存');
  await expect(row(admin, 'e2e-a')).toContainText('已下架');
  await expect(row(admin, 'e2e-a').getByTestId('plan-monthly')).toContainText(
    '¥12.34 / 30 天 / 50 GiB',
  );
  // 12.34 x 12 = 148.08; x 90% = 133.272 -> 133.27; / 12 = 11.1058 -> 11.10
  await expect(row(admin, 'e2e-a').getByTestId('plan-yearly')).toContainText('¥133.27');
  expect(await row(admin, 'e2e-a').locator('i, script').count()).toBe(0);

  // Price up and yearly on: the preview agrees with what the server then returns.
  await row(admin, 'e2e-a').getByRole('button', { name: /^编辑/ }).click();
  modal = admin.getByRole('dialog', { name: /^编辑套餐/ });
  await expect(modal.getByLabel('套餐 ID')).toHaveAttribute('readonly', '');
  await modal.getByLabel('月付价格（元）').fill('20.01');
  // 20.01 x 12 = 240.12; x 90% = 216.108 -> 216.10; saving 24.02; 216.10 / 12 = 18.008 -> 18.00
  const preview = modal.getByTestId('yearly-preview');
  await expect(preview.getByTestId('preview-original')).toHaveText('¥240.12');
  await expect(preview.getByTestId('preview-price')).toHaveText('¥216.10');
  await expect(preview.getByTestId('preview-saving')).toHaveText('¥24.02');
  await expect(preview.getByTestId('preview-monthly')).toHaveText('¥18.00');
  await shot(admin, 'plans-form-preview');
  await modal.getByRole('button', { name: '保存' }).click();
  const confirm = admin.getByRole('dialog', { name: '确认保存修改' });
  await expect(confirm).toContainText('月付价格将由 ¥12.34 改为 ¥20.01');
  await confirm.getByRole('button', { name: '确认保存' }).click();
  await expect(admin.getByTestId('plan-saved')).toContainText('服务端年付价：¥216.10');
  await expect(admin.getByTestId('plan-saved')).toContainText('立省 ¥24.02');
  await expect(row(admin, 'e2e-a').getByTestId('plan-yearly')).toContainText('¥216.10');
  await expect(row(admin, 'e2e-a').getByTestId('plan-yearly')).toContainText('9 折，跟随全局');
  await shot(admin, 'plans-list');

  // Not on the shelf yet: an ordinary user does not see it. Publish: it appears at once.
  const { context, page: user } = await visitor(browser, fixture.emails.kyle);
  await user.goto('/plans');
  await expect(user.getByText('进阶计划').first()).toBeVisible();
  await expect(user.getByText('E2E <i>套餐</i> A')).toHaveCount(0);
  await row(admin, 'e2e-a').getByRole('button', { name: /^编辑/ }).click();
  modal = admin.getByRole('dialog', { name: /^编辑套餐/ });
  await modal.getByLabel('上架').check({ force: true });
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(row(admin, 'e2e-a')).not.toContainText('已下架');
  await user.reload();
  await expect(user.getByText('E2E <i>套餐</i> A').first()).toBeVisible();
  await expect(user.getByText('¥20.01').first()).toBeVisible();
  const publicPlans = (await call(user, '/plans')).body as { plans: Record<string, unknown>[] };
  const published = publicPlans.plans.find((p) => p.id === 'e2e-a');
  expect(published).toBeTruthy();
  expect(published).not.toHaveProperty('note');
  // Withdraw: the dialog states the consequences, then the plan disappears.
  await row(admin, 'e2e-a').getByRole('button', { name: /^编辑/ }).click();
  modal = admin.getByRole('dialog', { name: /^编辑套餐/ });
  await modal.getByLabel('上架').uncheck({ force: true });
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(admin.getByRole('dialog', { name: '确认保存修改' })).toContainText(
    '下架后 /plans 不再展示；现有 0 位有效订阅用户不受影响',
  );
  await admin
    .getByRole('dialog', { name: '确认保存修改' })
    .getByRole('button', { name: '确认保存' })
    .click();
  await expect(row(admin, 'e2e-a')).toContainText('已下架');
  await user.reload();
  await expect(user.getByText('进阶计划').first()).toBeVisible();
  await expect(user.getByText('E2E <i>套餐</i> A')).toHaveCount(0);
  await context.close();
});

test('S2-02 invalid input is stopped in the form, and refused by the API with the field', async () => {
  await admin.goto('/admin/console/plans');
  let posts = 0;
  admin.on('request', (r) => {
    if (r.method() === 'POST' && /\/admin\/console\/plans/.test(r.url())) posts++;
  });
  await admin.getByRole('button', { name: '新建套餐' }).click();
  const modal = admin.getByRole('dialog', { name: '新建套餐' });
  await modal.getByLabel('套餐 ID').fill('E2E-Bad');
  await modal.getByLabel('名称', { exact: true }).fill('');
  await modal.getByLabel('月付价格（元）').fill('1.234');
  await modal.getByLabel('月付有效天数').fill('366');
  await modal.getByLabel('年付折扣（%）').fill('9');
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(modal).toContainText('套餐 ID 须以小写字母或数字开头');
  await expect(modal).toContainText('名称不能为空');
  await expect(modal).toContainText('月付价格须为 0.01–100000.00 元，最多两位小数');
  await expect(modal).toContainText('月付有效天数须为 1–365');
  await expect(modal).toContainText('年付折扣须为 50–100 的整数百分比');
  expect(posts).toBe(0);
  await shot(admin, 'plans-form-errors');
  for (const bad of ['-1', '100000.01', '0', '1e3', '12.']) {
    await modal.getByLabel('月付价格（元）').fill(bad);
    await modal.getByRole('button', { name: '保存' }).click();
    await expect(modal).toContainText('月付价格须为 0.01–100000.00 元');
  }
  await modal.getByLabel('名称', { exact: true }).fill('x'.repeat(41));
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(modal).toContainText('名称最多 40 个字符');
  expect(posts).toBe(0);

  // A duplicate ID is the server's answer, shown at the ID field.
  await modal.getByLabel('套餐 ID').fill('e2e-a');
  await modal.getByLabel('名称', { exact: true }).fill('重复');
  await modal.getByLabel('月付价格（元）').fill('5');
  await modal.getByLabel('月付有效天数').fill('30');
  await modal.getByLabel('年付折扣（%）').fill('');
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(modal).toContainText('套餐 ID 已存在');
  await modal.getByRole('button', { name: '取消' }).click();

  // Straight to the API, the same rules answer with the field that is wrong.
  const cases: [Record<string, unknown>, string][] = [
    [{ name: '' }, 'name'],
    [{ name: 'x'.repeat(41) }, 'name'],
    [{ price_cents: 10_000_001 }, 'price_cents'],
    [{ price_cents: -5 }, 'price_cents'],
    [{ duration_days: 366 }, 'duration_days'],
    [{ yearly_discount_percent: 9 }, 'yearly_discount_percent'],
    [{ yearly_discount_percent: 101 }, 'yearly_discount_percent'],
    [{ id: 'E2E-Bad' }, 'id'],
    [{ max_devices: 101 }, 'max_devices'],
  ];
  for (const [patch, field] of cases) {
    const r = await call(admin, '/admin/console/plans', 'POST', {
      ...terms('e2e-bad', 'e2e bad'),
      ...patch,
    });
    expect(r.status, JSON.stringify(patch)).toBe(400);
    expect(r.body.error.code).toBe('invalid_plan');
    expect(r.body.error.field).toBe(field);
  }
  expect((await call(admin, '/admin/console/plans', 'POST', terms('e2e-a', 'again'))).status).toBe(
    409,
  );
  expect((await plansOf(admin)).plans.some((p) => p.id === 'e2e-bad')).toBe(false);
});

test('S2-03 a device quota above the global cap warns, and the list shows both numbers', async () => {
  const cap = (await plansOf(admin)).client_max_online_devices;
  await admin.goto('/admin/console/plans');
  await admin.getByRole('button', { name: '新建套餐' }).click();
  const modal = admin.getByRole('dialog', { name: '新建套餐' });
  await modal.getByLabel('套餐 ID').fill('e2e-b');
  await modal.getByLabel('名称', { exact: true }).fill('E2E 设备套餐');
  await modal.getByLabel('月付价格（元）').fill('5');
  await modal.getByLabel('设备额度').fill(String(cap));
  await expect(modal.getByTestId('device-warning')).toHaveCount(0);
  await modal.getByLabel('设备额度').fill(String(cap + 8));
  await expect(modal.getByTestId('device-warning')).toContainText(
    `全局上限为 ${cap}，用户实际最多同时在线 ${cap} 台`,
  );
  await modal.getByText('普通线路加专线').click();
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(row(admin, 'e2e-b').getByTestId('plan-devices')).toContainText(
    `设定 ${cap + 8} 台 · 实际 ${cap} 台`,
  );
  await expect(
    row(admin, 'e2e-b').getByRole('img', { name: '设备额度超过全局上限' }),
  ).toBeVisible();
  await expect(row(admin, 'e2e-b')).toContainText('普通线路 + 专线');
});

test('S2-04 a plan can be deleted only when nothing depends on it', async () => {
  // The payment fixture has no node catalog, so groups cannot be created for real here
  // (the Go test TestShopAdminPlanLifecycle covers the real refusal). The page is shown what the
  // server says about a plan that groups still list: five names and "and N more", then the
  // refusal that names them.
  const listed = '**/api/v1/admin/console/plans?include_deleted=false';
  await admin.route(listed, async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    for (const plan of body.plans)
      if (plan.id === 'e2e-b') {
        plan.groups = ['E2E 线路组', 'JP2', 'JP3', 'US1', 'US2'];
        plan.group_count = 7;
      }
    await route.fulfill({ response, json: body });
  });
  await admin.route('**/api/v1/admin/console/plans/e2e-b/delete', (route) =>
    route.fulfill({
      status: 409,
      json: {
        error: {
          code: 'plan_in_use',
          message: '无法删除：线路组 E2E 线路组、JP2',
          reasons: ['线路组 E2E 线路组、JP2'],
          groups: ['E2E 线路组', 'JP2'],
        },
      },
    }),
  );
  await admin.goto('/admin/console/plans');
  await expect(row(admin, 'e2e-b').getByTestId('plan-groups')).toHaveText(
    'E2E 线路组、JP2、JP3、US1、US2 等 7 个',
  );
  await row(admin, 'e2e-b').getByRole('button', { name: /^删除/ }).click();
  const dialog = admin.getByRole('dialog', { name: /^删除套餐/ });
  await dialog.getByRole('button', { name: '确认删除' }).click();
  await expect(dialog.getByTestId('plan-in-use')).toContainText('线路组 E2E 线路组、JP2');
  await expect(dialog.getByTestId('plan-in-use')).toContainText('请先在“节点管理”中');
  await shot(admin, 'plans-delete-refused');
  await dialog.getByRole('button', { name: '取消' }).click();

  // With nothing listing it, the delete goes through and the plan shows under "show deleted".
  await admin.unroute('**/api/v1/admin/console/plans/e2e-b/delete');
  await admin.unroute(listed);
  await admin.reload();
  await row(admin, 'e2e-b').getByRole('button', { name: /^删除/ }).click();
  await admin
    .getByRole('dialog', { name: /^删除套餐/ })
    .getByRole('button', { name: '确认删除' })
    .click();
  await expect(row(admin, 'e2e-b')).toHaveCount(0);
  await admin.getByLabel('显示已删除').check({ force: true });
  await expect(row(admin, 'e2e-b')).toContainText('已删除');
  await admin.getByLabel('显示已删除').uncheck({ force: true });

  // An on-shelf plan cannot be deleted at all, and one with a live subscription is refused.
  expect(
    (
      await call(
        admin,
        '/admin/console/plans',
        'POST',
        terms('e2e-c', 'E2E 订阅套餐', { active: true }),
      )
    ).status,
  ).toBe(201);
  const users = (
    await call(admin, '/admin/console/users?q=' + encodeURIComponent(fixture.emails.judy))
  ).body as {
    users: { id: string }[];
  };
  const expires = new Date(Date.now() + 30 * 86400_000).toISOString();
  expect(
    (
      await call(admin, `/admin/console/users/${users.users[0].id}/subscription`, 'POST', {
        version: 0,
        plan_id: 'e2e-c',
        expires_at: expires,
        used: 'keep',
      })
    ).status,
  ).toBe(200);
  await admin.reload();
  await expect(row(admin, 'e2e-c').getByRole('button', { name: /^删除/ })).toHaveCount(0);
  await row(admin, 'e2e-c').getByRole('button', { name: /^编辑/ }).click();
  let modal = admin.getByRole('dialog', { name: /^编辑套餐/ });
  await modal.getByLabel('上架').uncheck({ force: true });
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(admin.getByRole('dialog', { name: '确认保存修改' })).toContainText(
    '现有 1 位有效订阅用户不受影响，但在有效期内无法续费',
  );
  await admin
    .getByRole('dialog', { name: '确认保存修改' })
    .getByRole('button', { name: '确认保存' })
    .click();
  await expect(admin.getByTestId('plan-saved')).toContainText(
    '修改只对新订单生效，现有 1 位订阅用户保持原有权益',
  );
  await expect(row(admin, 'e2e-c').getByText('已下架').first()).toBeVisible();
  await expect(row(admin, 'e2e-c')).toContainText('1');
  await row(admin, 'e2e-c').getByRole('button', { name: /^删除/ }).click();
  modal = admin.getByRole('dialog', { name: /^删除套餐/ });
  await modal.getByRole('button', { name: '确认删除' }).click();
  await expect(modal.getByTestId('plan-in-use')).toContainText('有效订阅 1 个');
  await modal.getByRole('button', { name: '取消' }).click();
});

test('S2-05 the later of two concurrent edits sees the conflict and, after a refresh, the other change', async ({
  browser,
}) => {
  expect(
    (await call(admin, '/admin/console/plans', 'POST', terms('e2e-d', 'E2E 冲突套餐'))).status,
  ).toBe(201);
  await admin.goto('/admin/console/plans');
  await row(admin, 'e2e-d').getByRole('button', { name: /^编辑/ }).click();
  const mine = admin.getByRole('dialog', { name: /^编辑套餐/ });

  const { context, page: other } = await visitor(browser, fixture.emails.boss);
  await other.goto('/admin/console/plans');
  await row(other, 'e2e-d').getByRole('button', { name: /^编辑/ }).click();
  const theirs = other.getByRole('dialog', { name: /^编辑套餐/ });
  await theirs.getByLabel('名称', { exact: true }).fill('E2E 冲突套餐（对方已改）');
  await theirs.getByRole('button', { name: '保存' }).click();
  await expect(other.getByTestId('plan-saved')).toBeVisible();
  await context.close();

  await mine.getByLabel('排序').fill('901');
  await mine.getByRole('button', { name: '保存' }).click();
  await expect(mine.getByTestId('console-error')).toContainText(
    '套餐已被其他管理员修改，请刷新后重试',
  );
  await mine.getByRole('button', { name: '刷新最新数据' }).click();
  await expect(row(admin, 'e2e-d')).toContainText('E2E 冲突套餐（对方已改）');
  await expect(row(admin, 'e2e-d')).toContainText('900');
  // Saving with nothing changed is answered, not applied.
  await row(admin, 'e2e-d').getByRole('button', { name: /^编辑/ }).click();
  const again = admin.getByRole('dialog', { name: /^编辑套餐/ });
  await again.getByRole('button', { name: '保存' }).click();
  await expect(again.getByTestId('console-error')).toContainText('没有任何改动');
  await again.getByRole('button', { name: '取消' }).click();
});

test('S2-06 the global yearly discount: the confirmation compares prices; own discounts stay', async () => {
  expect(
    (
      await call(
        admin,
        '/admin/console/plans',
        'POST',
        terms('e2e-e', 'E2E 跟随全局', { active: true }),
      )
    ).status,
  ).toBe(201);
  expect(
    (
      await call(
        admin,
        '/admin/console/plans',
        'POST',
        terms('e2e-f', 'E2E 单独折扣', { active: true, yearly_discount_percent: 80 }),
      )
    ).status,
  ).toBe(201);
  await admin.goto('/admin/console/plans');
  // 19.90 x 12 = 238.80: x 90% = 214.92, x 85% = 202.98 (floored); own 80% = 191.04.
  await expect(row(admin, 'e2e-e').getByTestId('plan-yearly')).toContainText('¥214.92');
  await expect(row(admin, 'e2e-f').getByTestId('plan-yearly')).toContainText('¥191.04');
  await expect(row(admin, 'e2e-f').getByTestId('plan-yearly')).toContainText('8 折，单独设置');
  await admin.getByLabel('全局年付折扣（%）').fill('85');
  await admin.getByRole('button', { name: '修改折扣' }).click();
  const confirm = admin.getByRole('dialog', { name: '修改全局年付折扣' });
  const compare = confirm.getByTestId('discount-compare');
  await expect(compare.locator('[data-plan-id="e2e-e"]')).toContainText('¥214.92');
  await expect(compare.locator('[data-plan-id="e2e-e"]')).toContainText('¥202.98');
  await expect(compare.locator('[data-plan-id="e2e-f"]')).toContainText('单独设置，不变');
  await expect(compare.locator('[data-plan-id="e2e-f"]')).not.toContainText('¥202.98');
  await shot(admin, 'plans-discount-confirm');
  await confirm.getByRole('button', { name: '确认修改' }).click();
  await expect(admin.getByTestId('discount-current')).toContainText('年付默认 8.5 折，省 15%');
  await expect(row(admin, 'e2e-e').getByTestId('plan-yearly')).toContainText('¥202.98');
  await expect(row(admin, 'e2e-f').getByTestId('plan-yearly')).toContainText('¥191.04');
  // Back to 90 through the page.
  await admin.getByLabel('全局年付折扣（%）').fill('90');
  await admin.getByRole('button', { name: '修改折扣' }).click();
  await admin
    .getByRole('dialog', { name: '修改全局年付折扣' })
    .getByRole('button', { name: '确认修改' })
    .click();
  await expect(admin.getByTestId('discount-current')).toContainText('年付默认 9 折，省 10%');
  expect((await plansOf(admin)).settings.yearly_discount_percent).toBe(90);
  // The floor of the range, typed by mistake as "9", is stopped.
  await admin.getByLabel('全局年付折扣（%）').fill('9');
  await admin.getByRole('button', { name: '修改折扣' }).click();
  await expect(admin.getByText('年付折扣须为 50–100 的整数百分比')).toBeVisible();
});

test('S2-07 other pages read the administrator plan list; coupons get a billing cycle', async () => {
  // Node management (its catalog is not enabled in this fixture, so its two own endpoints are
  // answered empty; the plan list is the real one): an off-shelf plan is offered with a label,
  // a deleted one is not.
  await admin.route('**/api/v1/admin/node-groups', (route) =>
    route.fulfill({
      json: {
        groups: [],
        server_time: new Date().toISOString(),
        max_groups: 128,
        max_servers: 512,
      },
    }),
  );
  await admin.route('**/api/v1/admin/nodes', (route) =>
    route.fulfill({ json: { supported_protocols: [] } }),
  );
  await admin.goto('/admin/nodes');
  await admin.getByRole('button', { name: '新建节点组', exact: true }).click();
  const group = admin.getByRole('dialog', { name: '新建节点组' });
  await group.getByLabel('限定套餐').click({ force: true });
  await expect(admin.getByRole('option', { name: 'E2E 订阅套餐（已下架）' })).toBeVisible();
  await expect(admin.getByRole('option', { name: /E2E 设备套餐/ })).toHaveCount(0);
  await admin.keyboard.press('Escape');
  await group.getByRole('button', { name: '取消' }).click();
  await admin.unroute('**/api/v1/admin/node-groups');
  await admin.unroute('**/api/v1/admin/nodes');

  // User management: the change-plan modal.
  await admin.goto('/admin/console/users?q=' + encodeURIComponent(fixture.emails.kyle));
  await admin.getByRole('button', { name: `查看 ${fixture.emails.kyle}` }).click();
  await admin
    .getByRole('dialog', { name: /^用户详情/ })
    .getByRole('button', { name: '修改套餐' })
    .click();
  const change = admin.getByRole('dialog', { name: /^修改套餐/ });
  await change.getByLabel('套餐', { exact: true }).click({ force: true });
  await expect(admin.getByRole('option', { name: /E2E 订阅套餐（已下架）/ })).toBeVisible();
  await expect(admin.getByRole('option', { name: /E2E 设备套餐/ })).toHaveCount(0);
  await expect(admin.getByRole('option', { name: /E2E 跟随全局/ })).toBeVisible();
  await admin.keyboard.press('Escape');
  await change.getByRole('button', { name: '取消' }).click();

  // Coupons: the cycle can be set and is listed.
  await admin.goto('/admin/console/coupons');
  await admin.getByRole('button', { name: '新建优惠码' }).click();
  let modal = admin.getByRole('dialog', { name: '新建优惠码' });
  await modal.getByLabel('优惠码', { exact: true }).fill('E2E-YEARLY');
  await modal.getByLabel('减价比例（%）').fill('10');
  await modal.getByLabel('适用周期').click({ force: true });
  await admin.getByRole('option', { name: '仅年付' }).click();
  await modal.getByRole('button', { name: '保存' }).click();
  const yearly = admin.getByTestId('coupon-row').filter({ hasText: 'E2E-YEARLY' });
  await expect(yearly.getByTestId('coupon-cycles')).toHaveText('仅年付');

  // A coupon that still lists a plan which is deleted afterwards cannot be saved again until
  // the plan is taken off its list.
  expect(
    (await call(admin, '/admin/console/plans', 'POST', terms('e2e-h', 'E2E 将删除套餐'))).status,
  ).toBe(201);
  await admin.reload();
  await admin.getByRole('button', { name: '新建优惠码' }).click();
  modal = admin.getByRole('dialog', { name: '新建优惠码' });
  await modal.getByLabel('优惠码', { exact: true }).fill('E2E-REF');
  await modal.getByLabel('减价比例（%）').fill('10');
  await modal.getByLabel('适用套餐').click({ force: true });
  await admin.getByRole('option', { name: 'E2E 将删除套餐（已下架）' }).click();
  await admin.keyboard.press('Escape');
  await modal.getByRole('button', { name: '保存' }).click();
  const ref = admin.getByTestId('coupon-row').filter({ hasText: 'E2E-REF' });
  await expect(ref).toContainText('E2E 将删除套餐（已下架）');
  const state = await plansOf(admin);
  const target = state.plans.find((p) => p.id === 'e2e-h')!;
  expect(
    (await call(admin, '/admin/console/plans/e2e-h/delete', 'POST', { version: target.version }))
      .status,
  ).toBe(200);
  await admin.reload();
  const refAgain = admin.getByTestId('coupon-row').filter({ hasText: 'E2E-REF' });
  await expect(refAgain).toContainText('E2E 将删除套餐（已删除）');
  await refAgain.getByRole('button', { name: '编辑 E2E-REF' }).click();
  modal = admin.getByRole('dialog', { name: /^编辑优惠码/ });
  await modal.getByLabel('备注').fill('改一下备注');
  await modal.getByRole('button', { name: '保存' }).click();
  await expect(modal.getByTestId('console-error')).toContainText(
    '请先从“适用套餐”中移除已删除的套餐',
  );
  await modal.getByRole('button', { name: '取消' }).click();
});

test('S2-07 the order console shows the cycle column and filter', async () => {
  await admin.goto('/admin/console/orders');
  await expect(admin.getByRole('columnheader', { name: '周期' })).toBeVisible();
  await admin.goto('/admin/console/orders?billing_cycle=yearly');
  await expect(admin.getByRole('textbox', { name: '付费周期' })).toHaveValue('年付');
  const api = await call(admin, '/admin/console/orders?billing_cycle=yearly');
  expect(api.status).toBe(200);
  for (const o of (api.body as { orders: { billing_cycle: string }[] }).orders)
    expect(o.billing_cycle).toBe('yearly');
});

test('S2-07 an order opens with its cycle and entitlement snapshot', async () => {
  await admin.goto('/admin/console/orders');
  const first = admin.getByTestId('order-row').first();
  await expect(first).toBeVisible();
  await expect(first.getByTestId('order-cycle')).toHaveText(/月付|年付/);
  await first.getByRole('button', { name: /^查看订单/ }).click();
  const detail = admin.getByTestId('order-detail');
  await expect(detail).toContainText('付费周期');
  await expect(detail).toContainText('权益快照');
});
