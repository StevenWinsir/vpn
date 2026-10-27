import { expect, test, type Browser, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { mkdir, readFile } from 'node:fs/promises';
import { overflowing } from './layout';

// Browser acceptance for the referral programme (handoff_coupon.md round 2, R2-01 … R2-08)
// against the real API on a private PostgreSQL and the local fake gateway
// (scripts/test-payment-web.py). The fixture runs the referral worker every second.
//
// The seeded plans are never changed: this spec creates its own e2e-ref-* plan through the
// administrator API (29.00 yuan, so the example of the plan is reproduced literally) and takes
// it off sale at the end. New accounts are registered through the real e-mail code flow.
type Fixture = {
  api: string;
  gateway: string;
  password: string;
  control: string;
  emails: Record<'refa' | 'refd' | 'root', string>;
};
type FixtureOrder = { id: string; trade_no: string; status: string; amount_cents: number };

const GiB = 1024 ** 3;
const baseURL = process.env.PLAYWRIGHT_BASE_URL!;
const codePattern = /^[2-9A-HJ-KM-NP-Z]{8}$/;

async function shot(page: Page, name: string) {
  const dir = `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${name}.png`, fullPage: true, animations: 'disabled' });
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
const ordersOf = async (email: string) =>
  (await control('orders?email=' + encodeURIComponent(email))) as FixtureOrder[];
const lastOrder = async (email: string) => (await ordersOf(email)).at(-1)!;
const subscriptionOf = async (email: string) =>
  (await control('subscription?email=' + encodeURIComponent(email))) as {
    plan_id: string;
    traffic_limit_bytes: number;
  };
const pay = (tradeNo: string) => control(`pay?trade_no=${tradeNo}`, 'POST');

async function mailCode(to: string): Promise<string> {
  for (let i = 0; i < 100; i++) {
    const response = await fetch(fixture.api + '/fixture/mail?to=' + encodeURIComponent(to), {
      headers: { 'X-Acceptance-Control': fixture.control },
    });
    if (response.ok) return ((await response.json()) as { code: string }).code;
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error('No registration code was delivered');
}

async function login(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

// A fresh visitor with an empty browser profile.
async function visitor(browser: Browser, width = 1440) {
  const context = await browser.newContext({
    baseURL,
    viewport: { width, height: 1000 },
    permissions: ['clipboard-read', 'clipboard-write'],
  });
  return { context, page: await context.newPage() };
}

const unique = (prefix: string) => `${prefix}-${randomUUID().slice(0, 8)}@gmail.com`;
const registrationPassword = 'Referral-Test-2026';
// The registration form as a person fills it. `field` is what is typed into the optional
// referral field (null: leave it alone, as for a plain visit).
async function register(
  page: Page,
  email: string,
  path = '/register',
  field: string | null = null,
) {
  await page.goto(path);
  await page.getByLabel('姓名 / 昵称').fill('推荐测试');
  await page.getByLabel('邮箱地址').fill(email);
  await page.getByRole('button', { name: '获取验证码' }).click();
  await expect(page.getByText('如果该邮箱可以注册')).toBeVisible();
  await page.getByLabel('邮箱验证码').fill(await mailCode(email));
  await page.locator('input[type="password"]').fill(registrationPassword);
  if (field !== null) await page.getByLabel('推荐码（选填）').fill(field);
  await page.getByRole('button', { name: '创建账户' }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}
// The body the browser sent to POST /auth/register, to see exactly what the field contributes.
function watchRegistration(page: Page) {
  const bodies: Record<string, unknown>[] = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().endsWith('/auth/register'))
      bodies.push(JSON.parse(request.postData() ?? '{}'));
  });
  return bodies;
}

// Administrator calls go through the signed-in page itself, like the real console.
let adminPage: Page;
type Call = { status: number; json: any }; // eslint-disable-line @typescript-eslint/no-explicit-any
async function admin(path: string, method = 'GET', body?: unknown): Promise<Call> {
  return adminPage.evaluate(
    async ({ path, method, body }) => {
      const go = () =>
        fetch('/api/v1' + path, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: body === undefined ? undefined : JSON.stringify(body),
        });
      let response = await go();
      if (response.status === 401) {
        await fetch('/api/v1/auth/refresh', { method: 'POST' });
        response = await go();
      }
      return { status: response.status, json: await response.json().catch(() => null) };
    },
    { path, method, body },
  );
}
const planBody = (over: Record<string, unknown>) => ({
  name: 'E2E',
  description: '',
  note: '',
  price_cents: 2900,
  duration_days: 30,
  traffic_bytes: 10 * GiB,
  max_devices: 2,
  allow_dedicated: false,
  yearly_enabled: false,
  yearly_duration_days: 365,
  yearly_discount_percent: null,
  featured: false,
  sort: 100,
  active: true,
  ...over,
});

const plan = 'e2e-ref-29';
const planName = 'E2E 推荐套餐';
test.describe.configure({ mode: 'serial' });
test.beforeAll(async ({ browser }) => {
  // A notice left published by another spec would cover the pages below.
  await control('archive-announcements');
  const context = await browser.newContext({ baseURL });
  adminPage = await context.newPage();
  await login(adminPage, fixture.emails.root);
  const created = await admin(
    '/admin/console/plans',
    'POST',
    planBody({ id: plan, name: planName, description: '端到端测试套餐' }),
  );
  expect([200, 201], JSON.stringify(created.json)).toContain(created.status);
});
test.afterAll(async () => {
  const list = await admin('/admin/console/plans').catch(() => null);
  const current = list?.json?.plans?.find((p: { id: string }) => p.id === plan);
  if (current) {
    const keys = Object.keys(planBody({}));
    await admin(`/admin/console/plans/${plan}`, 'POST', {
      ...Object.fromEntries(keys.map((k) => [k, current[k]])),
      active: false,
      version: current.version,
    }).catch(() => undefined);
  }
  await adminPage?.context().close();
});

const dialog = (page: Page) => page.getByRole('dialog');
// Buys the 29.00 yuan plan (or renews it) through the checkout dialog and the fake gateway.
async function buy(page: Page, email: string) {
  const before = (await ordersOf(email)).length;
  await page.goto('/plans');
  await page.getByRole('button', { name: new RegExp(`^(购买|续费) ${planName}$`) }).click();
  await expect(dialog(page).getByLabel('应付金额')).toHaveText('¥29.00');
  await dialog(page).getByRole('radio', { name: '微信支付' }).click();
  await dialog(page).getByRole('button', { name: '去支付', exact: true }).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  const order = await lastOrder(email);
  expect(await ordersOf(email)).toHaveLength(before + 1);
  expect(order).toMatchObject({ status: 'pending', amount_cents: 2900 });
  await pay(order.trade_no);
  await page.waitForURL(/\/pay\/result\?order=/, { timeout: 30000 });
  await expect(page.getByRole('heading', { name: '已到账', exact: true })).toBeVisible();
}

const referralApi = (page: Page) =>
  page.evaluate(async () => {
    const [summary, list] = await Promise.all([
      fetch('/api/v1/me/referral').then((r) => r.json()),
      fetch('/api/v1/me/referrals').then((r) => r.json()),
    ]);
    return { summary, list };
  });
const records = (page: Page) => page.getByTestId('referral-records');
// The page refreshes itself every 30 seconds; the test asks for a new look instead of waiting.
async function eventually(page: Page, check: () => Promise<void>) {
  await expect(async () => {
    await page.reload();
    await check();
  }).toPass({ timeout: 40000, intervals: [500, 1000, 2000] });
}

let aCode = '';
let aLink = '';
const bEmail = unique('bb');
const cEmail = unique('cc');

test('R2-01 the example of the plan: B pays 29.00 yuan and A gets 29 GB', async ({
  page,
  browser,
  context,
}) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await login(page, fixture.emails.refa);
  await buy(page, fixture.emails.refa);
  expect((await subscriptionOf(fixture.emails.refa)).traffic_limit_bytes).toBe(10 * GiB);

  // A's page: the code, the invitation link, the rules and an honest empty state.
  await page.getByRole('link', { name: '推荐有礼', exact: true }).first().click();
  await expect(page).toHaveURL(/\/referral$/);
  await expect(page.getByRole('heading', { name: '推荐有礼', exact: true })).toBeVisible();
  aCode = (await page.getByTestId('referral-code').textContent())!.trim();
  expect(aCode).toMatch(codePattern);
  aLink = `${baseURL}/register?ref=${aCode}`;
  await expect(page.getByLabel('邀请链接', { exact: true })).toHaveValue(aLink);
  await expect(page.getByTestId('no-plan')).toHaveCount(0);
  await expect(page.getByTestId('referral-empty')).toBeVisible();
  await expect(page.getByText('1 元 = 1 GB')).toBeVisible();
  await page.getByRole('button', { name: '复制推荐码' }).click();
  await expect(page.getByTestId('copy-notice')).toHaveText('推荐码已复制');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(aCode);
  await page.getByRole('button', { name: '复制邀请链接' }).first().click();
  await expect(page.getByTestId('copy-notice')).toHaveText('邀请链接已复制');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(aLink);
  // The code is stable: a reload shows the same one.
  await page.reload();
  await expect(page.getByTestId('referral-code')).toHaveText(aCode);

  // B opens the link in a clean browser: the code is already in the field.
  const b = await visitor(browser);
  const sent = watchRegistration(b.page);
  await b.page.goto(aLink);
  await expect(b.page.getByLabel('推荐码（选填）')).toHaveValue(aCode);
  await shot(b.page, 'referral-register-prefilled-1440');
  await register(b.page, bEmail, `/register?ref=${aCode.toLowerCase()}`);
  expect(sent).toHaveLength(1);
  expect(sent[0].referral_code).toBe(aCode);

  // A sees B right away, with a masked address and no consumption yet.
  await eventually(page, async () => {
    await expect(records(page)).toContainText('bb***@gmail.com');
  });
  await expect(records(page)).not.toContainText(bEmail);
  await expect(records(page)).toContainText('已注册·未消费');
  await expect(page.getByTestId('referral-stats')).toContainText('邀请注册1');

  // B pays 29.00 yuan; nothing is done on A's side.
  await buy(b.page, bEmail);
  await eventually(page, async () => {
    await expect(records(page)).toContainText('已发放 +29 GB', { timeout: 2000 });
  });
  await expect(page.getByTestId('referral-stats')).toContainText('已消费1');
  await expect(page.getByTestId('referral-stats')).toContainText('待发放0');
  await expect(page.getByTestId('referral-stats')).toContainText('累计获得流量29 GB');
  await shot(page, 'referral-page-1440');

  // A's allowance grew by exactly 29 GB and the overview says so.
  expect((await subscriptionOf(fixture.emails.refa)).traffic_limit_bytes).toBe(39 * GiB);
  await page.goto('/dashboard');
  await expect(page.getByText('含推荐奖励 29 GB')).toBeVisible();
  await expect(page.getByText('39 GB', { exact: true }).first()).toBeVisible();
  // The overview carries no referral card any more (handoff_ui.md R2); the sidebar leads there.
  await expect(page.getByTestId('referral-card')).toHaveCount(0);
  await shot(page, 'referral-dashboard-1440');
  await page.getByRole('link', { name: '推荐有礼', exact: true }).click();
  await expect(page).toHaveURL(/\/referral$/);
  await b.context.close();
});

test('R2-02 only the first purchase counts', async ({ page, browser }) => {
  // B signs in with the password chosen at registration, then buys the plan again (a renewal).
  const again = await visitor(browser);
  await again.page.goto('/login');
  await again.page.getByLabel('邮箱地址').fill(bEmail);
  await again.page.locator('input[type="password"]').fill(registrationPassword);
  await again.page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(again.page).toHaveURL(/\/dashboard$/);
  await buy(again.page, bEmail);
  expect(await ordersOf(bEmail)).toHaveLength(2);
  await again.context.close();
  // The worker has had plenty of one-second turns; nothing moved on A's side.
  await new Promise((resolve) => setTimeout(resolve, 4000));
  expect((await subscriptionOf(fixture.emails.refa)).traffic_limit_bytes).toBe(39 * GiB);
  await login(page, fixture.emails.refa);
  const { summary, list } = await referralApi(page);
  expect(list.total).toBe(1);
  expect(summary.stats).toMatchObject({
    registered_count: 1,
    purchased_count: 1,
    rewarded_count: 1,
    rewarded_bytes: 29 * GiB,
  });
});

test('R2-03 a referrer without a plan gets nothing, and buying one later does not bring it back', async ({
  page,
  browser,
}) => {
  await login(page, fixture.emails.refd);
  await page.goto('/referral');
  const warning = page.getByTestId('no-plan');
  await expect(warning).toContainText('你当前没有有效套餐，好友首单奖励将不生效');
  await expect(warning.getByRole('link', { name: '去订阅' })).toHaveAttribute('href', '/plans');
  const dCode = (await page.getByTestId('referral-code').textContent())!.trim();
  expect(dCode).toMatch(codePattern);
  await shot(page, 'referral-no-plan-1440');

  const c = await visitor(browser);
  await register(c.page, cEmail, `/register?ref=${dCode}`);
  await buy(c.page, cEmail);
  await c.context.close();
  await eventually(page, async () => {
    await expect(records(page)).toContainText('未生效（发放时无有效套餐）', { timeout: 2000 });
  });
  await expect(records(page)).toContainText('cc***@gmail.com');

  // D now buys the plan: the forfeited reward stays forfeited.
  await buy(page, fixture.emails.refd);
  await new Promise((resolve) => setTimeout(resolve, 4000));
  expect((await subscriptionOf(fixture.emails.refd)).traffic_limit_bytes).toBe(10 * GiB);
  await page.goto('/referral');
  await expect(page.getByTestId('no-plan')).toHaveCount(0);
  await expect(records(page)).toContainText('未生效（发放时无有效套餐）');
  await expect(page.getByTestId('referral-stats')).toContainText('累计获得流量0 MB');
});

test('R2-04 a wrong code is never mentioned; only a right one (typed any way) is recorded', async ({
  page,
  browser,
}) => {
  const attempts: { email: string; typed: string; kept: boolean }[] = [
    { email: unique('e1'), typed: '2222ABCD', kept: false }, // well formed, nobody owns it
    { email: unique('e2'), typed: '!!@@##$$ %%', kept: false }, // nonsense
    {
      email: unique('e3'),
      typed: aCode.slice(0, 4).toLowerCase() + ' ' + aCode.slice(4).toLowerCase(),
      kept: true,
    },
  ];
  for (const attempt of attempts) {
    const v = await visitor(browser);
    const sent = watchRegistration(v.page);
    await register(v.page, attempt.email, '/register', attempt.typed);
    // No error box, no dialog, and the person is simply inside.
    await expect(v.page.locator('.mantine-Alert-root')).toHaveCount(0);
    await expect(v.page.getByRole('dialog')).toHaveCount(0);
    expect(sent).toHaveLength(1);
    expect(typeof sent[0].referral_code).toBe('string');
    await v.context.close();
  }
  await login(page, fixture.emails.refa);
  const { list } = await referralApi(page);
  const masks = list.items.map((i: { email_masked: string }) => i.email_masked).sort();
  expect(masks).toEqual(['bb***@gmail.com', 'e3***@gmail.com']);
  expect(JSON.stringify(list)).not.toContain(bEmail);
  await page.goto('/referral');
  await expect(records(page)).toContainText('e3***@gmail.com');
  await expect(records(page)).not.toContainText('e1***');
  await expect(records(page)).not.toContainText('e2***');
});

test('R2-05 a backend without the programme: no field, no request field, no entry', async ({
  page,
  browser,
}) => {
  const v = await visitor(browser);
  const asked: string[] = [];
  v.page.on('request', (request) => {
    if (/\/me\/referral/.test(request.url())) asked.push(request.url());
  });
  // An older backend answers /meta without referral_enabled.
  await v.page.route('**/api/v1/meta', async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    delete body.referral_enabled;
    await route.fulfill({ response, json: body });
  });
  const sent = watchRegistration(v.page);
  const email = unique('e4');
  await v.page.goto(`/register?ref=${aCode}`);
  await expect(v.page.getByLabel('姓名 / 昵称')).toBeVisible();
  await expect(v.page.getByLabel('推荐码（选填）')).toHaveCount(0);
  await register(v.page, email, `/register?ref=${aCode}`);
  expect(sent).toHaveLength(1);
  expect(Object.keys(sent[0]).sort()).toEqual([
    'code',
    'email',
    'name',
    'password',
    'remember',
    'verification_id',
  ]);
  // Neither the sidebar entry nor the overview card shows, and nothing asks for them.
  await expect(v.page.getByRole('heading', { name: '还没有开通套餐' })).toBeVisible();
  await expect(v.page.getByRole('link', { name: '推荐有礼' })).toHaveCount(0);
  await expect(v.page.getByTestId('referral-card')).toHaveCount(0);
  await v.page.goto('/referral');
  await expect(v.page.getByText('推荐有礼暂时不可用')).toBeVisible();
  await expect(v.page.getByRole('link', { name: '推荐有礼' })).toHaveCount(0);
  expect(asked).toEqual([]);
  await v.context.close();

  await login(page, fixture.emails.refa);
  expect((await referralApi(page)).list.total).toBe(2);
});

test('R2-06 layout, copy fallback, states and a failing summary', async ({ page, browser }) => {
  await login(page, fixture.emails.refa);

  // 1440: the page has no horizontal overflow; the register page shows the field.
  await page.goto('/referral');
  await expect(records(page).getByRole('table', { name: '推荐记录' })).toBeVisible();
  expect(await overflowing(page)).toEqual([]);

  // A pending reward and an account that has since gone, as the list shows them.
  await page.route('**/api/v1/me/referrals?page=1', async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.items = [
      {
        email_masked: 'zh***@qq.com',
        registered_at: '2026-10-26T08:00:00Z',
        status: 'qualified',
        reward_bytes: 29 * GiB,
        granted_bytes: 0,
        granted_at: null,
        forfeit_reason: '',
        referee_deleted: false,
      },
      {
        email_masked: 'li***@qq.com',
        registered_at: '2026-10-25T08:00:00Z',
        status: 'rewarded',
        reward_bytes: 5 * GiB,
        granted_bytes: 5 * GiB,
        granted_at: '2026-10-25T09:00:00Z',
        forfeit_reason: '',
        referee_deleted: true,
      },
      {
        email_masked: 'wa***@qq.com',
        registered_at: '2026-10-24T08:00:00Z',
        status: 'forfeited',
        reward_bytes: 1 * GiB,
        granted_bytes: 0,
        granted_at: null,
        forfeit_reason: 'referrer_unavailable',
        referee_deleted: false,
      },
    ];
    body.total = 3;
    await route.fulfill({ response, json: body });
  });
  await page.reload();
  const table = records(page);
  await expect(table).toContainText('待发放（约 29 GB）');
  await expect(table).toContainText('已发放 +5 GB');
  await expect(table).toContainText('已注销');
  await expect(table).toContainText('未生效（账号不可用）');

  // 375: cards instead of a table, nothing leaves the screen.
  await page.setViewportSize({ width: 375, height: 800 });
  await page.reload();
  await expect(page.getByTestId('referral-cards')).toBeVisible();
  await expect(table.getByRole('table')).toHaveCount(0);
  await expect(page.getByTestId('referral-cards')).toContainText('li***@qq.com');
  expect(await overflowing(page)).toEqual([]);
  const code = (await page.getByTestId('referral-code').boundingBox())!;
  expect(code.x + code.width).toBeLessThanOrEqual(375);
  await shot(page, 'referral-page-375');
  await page.unroute('**/api/v1/me/referrals?page=1');
  await page.setViewportSize({ width: 1440, height: 1000 });

  // The clipboard refuses: the link is selected for a manual copy and the person is told.
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: () => Promise.reject(new Error('denied')) },
      configurable: true,
    });
  });
  await page.goto('/referral');
  await page.getByRole('button', { name: '复制邀请链接' }).first().click();
  await expect(page.getByTestId('copy-notice')).toContainText('已选中邀请链接，请手动复制');
  expect(
    await page.evaluate(() => {
      const input = document.activeElement as HTMLInputElement;
      return input.value.slice(input.selectionStart ?? 0, input.selectionEnd ?? 0);
    }),
  ).toBe(aLink);

  // The overview survives a failing summary: no card, no error box.
  const v = await visitor(browser);
  await login(v.page, fixture.emails.refa);
  await v.page.route('**/api/v1/me/referral', (route) => route.fulfill({ status: 503, json: {} }));
  await v.page.goto('/dashboard');
  await expect(v.page.getByText('含推荐奖励 29 GB')).toBeVisible();
  await expect(v.page.getByRole('heading', { name: planName, exact: true })).toBeVisible();
  await expect(v.page.getByTestId('referral-card')).toHaveCount(0);
  await expect(v.page.locator('.mantine-Alert-root')).toHaveCount(0);
  await v.context.close();

  // The register page at both widths.
  const wide = await visitor(browser);
  await wide.page.goto('/register');
  await expect(wide.page.getByLabel('推荐码（选填）')).toBeVisible();
  await expect(wide.page.getByLabel('推荐码（选填）')).toHaveValue('');
  await shot(wide.page, 'referral-register-1440');
  await wide.page.setViewportSize({ width: 375, height: 900 });
  await wide.page.reload();
  await expect(wide.page.getByLabel('推荐码（选填）')).toBeVisible();
  expect(await overflowing(wide.page)).toEqual([]);
  await shot(wide.page, 'referral-register-375');
  await wide.context.close();

  // Signed-out visitors are sent to sign in.
  const out = await visitor(browser);
  await out.page.goto('/referral');
  await expect(out.page).toHaveURL(/\/login/);
  await out.context.close();
});

test('R2-07 the console shows code, referrer and referrals; an ordinary user is refused', async ({
  page,
}) => {
  const find = async (email: string) => {
    const found = await admin('/admin/console/users?q=' + encodeURIComponent(email));
    expect(found.status).toBe(200);
    return found.json.users[0].id as string;
  };
  const aId = await find(fixture.emails.refa);
  const bId = await find(bEmail);

  await adminPage.goto('/admin/console/users?q=' + encodeURIComponent(fixture.emails.refa));
  await adminPage.getByRole('button', { name: `查看 ${fixture.emails.refa}` }).click();
  const section = adminPage.getByTestId('user-referrals');
  await expect(section.getByTestId('referral-user-code')).toHaveText(aCode);
  await expect(section).toContainText('无（未使用推荐码注册）');
  await expect(section).toContainText(
    '注册 2 · 已消费 1 · 待发放 0 · 已发放 1（29 GB） · 未生效 0',
  );
  const rows = section.getByRole('table', { name: '被推荐用户' });
  await expect(rows).toContainText(bEmail);
  await expect(rows).toContainText('已发放 +29 GB');
  await expect(rows).toContainText('¥29.00');
  await shot(adminPage, 'referral-console-user');

  // B's detail names A, and the link opens A.
  await adminPage.goto('/admin/console/users?user=' + bId);
  const link = adminPage.getByTestId('referrer-link');
  await expect(link).toHaveText(fixture.emails.refa);
  await link.click();
  await expect(adminPage).toHaveURL(new RegExp('user=' + aId));
  await expect(
    adminPage.getByTestId('user-referrals').getByTestId('referral-user-code'),
  ).toHaveText(aCode);

  // An ordinary account may not read it, not even its own.
  await login(page, fixture.emails.refa);
  const refused = await page.evaluate(async (id) => {
    const response = await fetch(`/api/v1/admin/console/users/${id}/referrals`);
    return response.status;
  }, aId);
  expect(refused).toBe(403);
});
