import { expect, test, type Browser, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import { bannedIn } from './copy-words';

// Browser acceptance for support tickets (handoff_ticket.md 15, round 2: T2-01 … T2-09). It runs
// against the real API on a private PostgreSQL, started by scripts/test-payment-web.py, whose
// fixture deletes expired tickets every second and can move a ticket's deletion time. The
// administrator and the user use separate browser contexts, so "real time" is what one sees of
// the other's actions without reloading.
type Fixture = {
  api: string;
  password: string;
  control: string;
  emails: Record<'ticketroot' | 'ticketa' | 'ticketb' | 'ticketc', string>;
};

let fixture: Fixture;
test.beforeAll(async () => {
  const filename = process.env.PAYMENT_FIXTURE;
  if (!filename?.startsWith('/tmp/vpn-payment-'))
    throw new Error('Private payment fixture required');
  fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
});

async function shot(page: Page, name: string, fullPage = false) {
  const dir = `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${name}.png`, animations: 'disabled', fullPage });
}

async function control(path: string, method = 'GET') {
  const response = await fetch(fixture.api + '/fixture/' + path, {
    method,
    headers: { 'X-Acceptance-Control': fixture.control },
  });
  if (!response.ok && response.status !== 204)
    throw new Error(`fixture ${path}: ${response.status}`);
  return response.status === 204 ? null : response.json();
}

async function signIn(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

async function visitor(browser: Browser, email: string, viewport = { width: 1440, height: 1000 }) {
  const context = await browser.newContext({ viewport });
  const page = await context.newPage();
  await signIn(page, email);
  return { context, page };
}

// A request from the page itself (its cookies, its origin), as the site would send it.
async function call(page: Page, method: string, path: string, body?: unknown) {
  return page.evaluate(
    async ({ method, path, body }) => {
      const response = await fetch('/api/v1' + path, {
        method,
        headers:
          body === undefined
            ? {}
            : { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      return { status: response.status, json: await response.json().catch(() => null) };
    },
    { method, path, body },
  );
}

// Stops every visibility-driven refresh of a page (lists, details, badges), so what it shows
// stays as it was until the page itself asks again.
async function hide(page: Page) {
  await page.evaluate(() =>
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' }),
  );
}
async function show(page: Page) {
  await page.evaluate(() => {
    delete (document as unknown as Record<string, unknown>).visibilityState;
    document.dispatchEvent(new Event('visibilitychange'));
  });
}

const ticketIdIn = (page: Page) => new URL(page.url()).searchParams.get('id') ?? '';
const nav = (page: Page) => page.getByRole('navigation').first();
const unreadBadge = (page: Page) => page.getByTestId('ticket-unread-badge');

async function compose(
  page: Page,
  { category, subject, body }: { category: RegExp; subject: string; body: string },
) {
  await page.getByRole('button', { name: '提交工单' }).first().click();
  const form = page.getByRole('dialog', { name: '提交工单' });
  await form.getByRole('radio', { name: category }).click();
  await form.getByLabel('标题').fill(subject);
  await form.getByLabel('问题描述').fill(body);
  return form;
}

async function submitTicket(
  page: Page,
  values: { category: RegExp; subject: string; body: string },
) {
  await page.goto('/tickets');
  const form = await compose(page, values);
  await form.getByRole('button', { name: '提交', exact: true }).click();
  await expect(page).toHaveURL(/\/tickets\?id=[0-9a-f-]{36}$/);
  await expect(page.getByTestId('ticket-detail')).toBeVisible();
  return ticketIdIn(page);
}

async function adminTickets(browser: Browser) {
  const admin = await visitor(browser, fixture.emails.ticketroot);
  await admin.page.goto('/admin/console/tickets');
  await expect(admin.page.getByRole('heading', { name: '管理后台' })).toBeVisible();
  return admin;
}

const queueRow = (page: Page, subject: string) =>
  page.getByTestId('console-ticket-row').filter({ hasText: subject });
const drawer = (page: Page) => page.getByTestId('console-ticket-detail');

// Requirements polled every 15 s (console) and 60 s (sidebar) take longer than the default.
// ticketa submits five tickets in all, the daily limit (D5); ticketb two.
test.describe.configure({ mode: 'serial', timeout: 240_000 });

const html =
  '<b>粗体</b><script>window.__ticketXss = 1</script><img src=x onerror="window.__ticketXss=2">';
const first = '付款后套餐没有开通';

test('T2-01 / T2-05 / T2-09: submit, the console sees it, support replies, the user reads and adds more', async ({
  browser,
}) => {
  // A gateway order of A's own, offered by the form.
  await control('pending-order?email=' + encodeURIComponent(fixture.emails.ticketa), 'POST');
  const a = await visitor(browser, fixture.emails.ticketa);
  const admin = await adminTickets(browser);
  await expect(admin.page.getByTestId('console-tickets-empty')).toBeVisible();
  await expect(admin.page.getByTestId('console-ticket-badge')).toHaveCount(0);
  await shot(admin.page, 'console-tickets-empty');

  // The entry sits after "推荐有礼", before "下载客户端".
  const entries = await nav(a.page).locator('a.nav-link').allInnerTexts();
  const at = entries.findIndex((text) => text.startsWith('我的工单'));
  expect(at).toBeGreaterThan(entries.findIndex((text) => text.startsWith('推荐有礼')));
  expect(at).toBeLessThan(entries.findIndex((text) => text.startsWith('下载客户端')));
  await nav(a.page).getByRole('link', { name: '我的工单' }).click();
  await expect(a.page).toHaveURL(/\/tickets$/);
  await expect(a.page).toHaveTitle(/^我的工单 · /);
  await expect(a.page.getByTestId('tickets-empty')).toBeVisible();
  await expect(a.page.getByText('工单自提交起保留 30 天，到期自动删除')).toBeVisible();

  // T2-05: field errors stay in the form and nothing is sent.
  const posts: string[] = [];
  a.page.on('request', (request) => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/me/tickets')
      posts.push(request.headers()['idempotency-key'] ?? '');
  });
  await a.page.getByRole('button', { name: '提交工单' }).first().click();
  const form = a.page.getByRole('dialog', { name: '提交工单' });
  await expect(form.getByPlaceholder(/复制诊断信息/)).toBeVisible();
  await form.getByRole('button', { name: '提交', exact: true }).click();
  await expect(form.getByText('请选择问题分类')).toBeVisible();
  await expect(form.getByText('请填写标题')).toBeVisible();
  await expect(form.getByText('请填写问题描述')).toBeVisible();
  await form.getByRole('radio', { name: /套餐、订单与支付/ }).click();
  await form.getByLabel('标题').fill('长'.repeat(61));
  await form.getByLabel('问题描述').fill('长'.repeat(3001));
  await form.getByRole('button', { name: '提交', exact: true }).click();
  await expect(form.getByText('标题最多 60 个字')).toBeVisible();
  await expect(form.getByText('问题描述最多 3000 个字')).toBeVisible();
  await expect(form.getByText('3001/3000')).toBeVisible();
  expect(posts).toEqual([]);

  // billing opens "关联订单"; the device field waits behind its link.
  await expect(form.getByLabel('使用的设备（选填）')).toHaveCount(0);
  await form.getByLabel('关联订单（选填）').click();
  const option = a.page.getByRole('option', { name: /进阶计划/ });
  const orderText = (await option.innerText()).trim();
  await option.click();
  await form.getByLabel('标题').fill(first);
  await form.getByLabel('问题描述').fill(`昨晚付款后套餐一直没有开通。\n页面片段：${html}`);
  await shot(a.page, 'ticket-form-1440');
  // A double click submits once (and a repeat would replay the same key anyway).
  await form.getByRole('button', { name: '提交', exact: true }).dblclick();
  await expect(a.page).toHaveURL(/\/tickets\?id=[0-9a-f-]{36}$/);
  const one = ticketIdIn(a.page);
  expect(new Set(posts).size).toBe(1);
  const detail = a.page.getByTestId('ticket-detail');
  await expect(detail.getByTestId('ticket-status')).toHaveText('等待客服回复');
  await expect(detail.getByTestId('ticket-no')).toHaveText(/^工单号 T\d{12}$/);
  await expect(detail.getByTestId('ticket-order')).toHaveText(orderText);
  await expect(detail.getByTestId('ticket-deletion')).toHaveText(
    /^将于 \d{4}-\d{2}-\d{2} \d{2}:\d{2} 自动删除$/,
  );
  // T2-09: the HTML is the user's text, shown as written and never run.
  const text = detail.locator('.ticket-text').first();
  await expect(text).toContainText(html);
  await expect(text.locator('b, script, img')).toHaveCount(0);
  expect(
    await a.page.evaluate(() => (window as unknown as { __ticketXss?: number }).__ticketXss),
  ).toBeUndefined();
  await shot(a.page, 'ticket-detail-1440', true);
  await a.page.getByTestId('back-to-list').click();
  const row = a.page.getByTestId('ticket-row').filter({ hasText: first });
  await expect(row).toHaveCount(1);
  await expect(row).toContainText('等待客服回复');
  await expect(row).toContainText('套餐、订单与支付');
  await expect(row).toContainText(/将于 .* 自动删除/);
  await shot(a.page, 'tickets-list-1440');

  // The console, never reloaded: the tab badge and the first row within one refresh.
  await expect(admin.page.getByTestId('console-ticket-badge')).toHaveText('1', { timeout: 25000 });
  await expect(admin.page.getByTestId('console-ticket-dot')).toBeVisible();
  await expect(admin.page.getByTestId('console-ticket-row').first()).toContainText(first);
  await expect(queueRow(admin.page, first)).toContainText(/刚刚提交|已等待 \d+ 分钟/);
  await expect(queueRow(admin.page, first)).toContainText(fixture.emails.ticketa);
  await shot(admin.page, 'console-tickets-1440');
  await queueRow(admin.page, first).click();
  await expect(admin.page).toHaveURL(new RegExp(`ticket=${one}`));
  await expect(drawer(admin.page).getByTestId('console-ticket-order')).toContainText('进阶计划');
  await expect(drawer(admin.page).getByTestId('console-ticket-order')).toHaveAttribute(
    'href',
    /\/admin\/console\/orders\?order=[0-9a-f-]{36}$/,
  );
  await expect(drawer(admin.page).getByTestId('console-ticket-user')).toHaveText(
    fixture.emails.ticketa,
  );
  await expect(drawer(admin.page).locator('.ticket-text').first()).toContainText(html);
  await drawer(admin.page).getByLabel('回复').fill('您好，已核实订单，请重新打开客户端查看套餐。');
  await drawer(admin.page).getByRole('button', { name: '发送回复' }).click();
  await expect(drawer(admin.page).getByTestId('console-ticket-status')).toHaveText('已回复');
  await expect(
    drawer(admin.page).getByTestId('console-ticket-message').filter({ hasText: '已核实订单' }),
  ).toContainText(`客服 · ${fixture.emails.ticketroot}`);
  await expect(admin.page.getByTestId('console-ticket-badge')).toHaveCount(0);
  await shot(admin.page, 'console-ticket-drawer-1440');

  // A's sidebar: the next page change asks at once.
  await nav(a.page).getByRole('link', { name: '总览' }).click();
  await expect(unreadBadge(a.page)).toHaveText('1', { timeout: 15000 });
  await nav(a.page)
    .getByRole('link', { name: /我的工单/ })
    .click();
  const unread = a.page.getByTestId('ticket-row').filter({ hasText: first });
  await expect(unread).toHaveClass(/ticket-unread/);
  await expect(unread).toContainText('客服已回复');
  await unread.getByRole('link', { name: first }).click();
  const reply = a.page.getByTestId('ticket-message').filter({ hasText: '已核实订单' });
  await expect(reply).toHaveAttribute('data-author', 'staff');
  await expect(reply).toContainText('客服');
  await expect(reply).not.toContainText(fixture.emails.ticketroot);
  await expect(unreadBadge(a.page)).toHaveCount(0);
  const page = await a.page.evaluate(() => document.body.innerText);
  expect(page).not.toContain('管理员');
  expect(bannedIn(page)).toEqual([]);

  // A adds more; the open drawer shows it within one detail refresh, back in the queue.
  await a.page.getByTestId('ticket-reply').fill('重新打开后还是显示未订阅。');
  await a.page.getByRole('button', { name: '发送', exact: true }).click();
  await expect(a.page.getByTestId('ticket-status')).toHaveText('等待客服回复');
  await expect(a.page.getByTestId('ticket-reply')).toHaveValue('');
  // handoff_message.md C4.
  await expect(a.page.getByTestId('toast').filter({ hasText: '回复已发送' })).toBeVisible();
  await expect(drawer(admin.page).getByTestId('console-ticket-news')).toHaveText(/用户有新消息/, {
    timeout: 20000,
  });
  await expect(drawer(admin.page).getByTestId('console-ticket-status')).toHaveText('待处理');
  await expect(admin.page.getByTestId('console-ticket-badge')).toHaveText('1', { timeout: 25000 });
  await a.context.close();
  await admin.context.close();
});

test('T2-02 / T2-03 / T2-04: closing without a reply, with a note, a stale close and the user closing', async ({
  browser,
}) => {
  const a = await visitor(browser, fixture.emails.ticketa);
  const admin = await adminTickets(browser);

  // T2-02: closed without a word.
  const quiet = '验证码一直收不到';
  const two = await submitTicket(a.page, {
    category: /账号与登录/,
    subject: quiet,
    body: '注册时验证码邮件一直没有收到。',
  });
  await expect(queueRow(admin.page, quiet)).toHaveCount(1, { timeout: 25000 });
  await queueRow(admin.page, quiet).click();
  await drawer(admin.page).getByRole('button', { name: '关闭工单' }).click();
  await admin.page
    .getByRole('dialog', { name: /^关闭工单/ })
    .getByRole('button', { name: '确认关闭' })
    .click();
  await expect(drawer(admin.page).getByTestId('console-ticket-status')).toHaveText('已关闭');
  await expect(drawer(admin.page)).toContainText(`由 ${fixture.emails.ticketroot} 关闭`);
  await a.page.reload();
  await expect(a.page.getByTestId('ticket-status')).toHaveText('已关闭（由客服关闭）');
  await expect(a.page.getByTestId('ticket-closed')).toContainText('客服已关闭此工单');
  await expect(a.page.getByTestId('ticket-reply')).toHaveCount(0);
  await expect(a.page.getByRole('button', { name: /关闭工单/ })).toHaveCount(0);
  expect(await a.page.locator('.ticket-text').count()).toBe(1);
  expect(two).not.toBe('');

  // T2-03: the user writes while the console still shows the older version.
  const stale = '客户端安装后打不开';
  const three = await submitTicket(a.page, {
    category: /客户端下载与安装/,
    subject: stale,
    body: '安装完成后双击没有反应。',
  });
  await admin.page.goto('/admin/console/tickets?ticket=' + three);
  await expect(drawer(admin.page).locator('.ticket-text')).toHaveCount(1);
  await hide(admin.page);
  expect(
    (
      await call(a.page, 'POST', `/me/tickets/${three}/messages`, {
        body: '补充：系统是 Windows 11。',
      })
    ).status,
  ).toBe(201);
  await drawer(admin.page).getByRole('button', { name: '关闭工单' }).click();
  const closing = admin.page.getByRole('dialog', { name: /^关闭工单/ });
  await closing.getByLabel('关闭说明（选填）').fill('已在新版本修复。');
  await closing.getByRole('button', { name: '确认关闭' }).click();
  await expect(drawer(admin.page).getByTestId('console-ticket-notice')).toContainText(
    '用户有新回复',
  );
  await expect(drawer(admin.page).locator('.ticket-text')).toHaveCount(2);
  await expect(drawer(admin.page).getByTestId('console-ticket-status')).toHaveText('待处理');
  await show(admin.page);

  // T2-02 with a note: the note is the last support message.
  await drawer(admin.page).getByRole('button', { name: '关闭工单' }).click();
  await closing.getByLabel('关闭说明（选填）').fill('新版本已修复该问题，请更新客户端。');
  await closing.getByRole('button', { name: '确认关闭' }).click();
  await expect(drawer(admin.page).getByTestId('console-ticket-status')).toHaveText('已关闭');
  await a.page.reload();
  await expect(a.page.getByTestId('ticket-status')).toHaveText('已关闭（由客服关闭）');
  const last = a.page.getByTestId('ticket-message').last();
  await expect(last).toHaveAttribute('data-author', 'staff');
  await expect(last).toContainText('新版本已修复该问题');

  await a.page.goto('/tickets');
  await a.page.locator('.mantine-SegmentedControl-label', { hasText: '已关闭' }).click();
  await expect(a.page.getByTestId('ticket-row')).toHaveCount(2);
  await a.context.close();

  // T2-04: the user closes a ticket; it leaves the waiting queue.
  const b = await visitor(browser, fixture.emails.ticketb);
  const own = '想确认流量计算方式';
  await submitTicket(b.page, { category: /其他问题/, subject: own, body: '倍率怎么算？' });
  await admin.page.goto('/admin/console/tickets');
  await expect(queueRow(admin.page, own)).toHaveCount(1, { timeout: 25000 });
  await b.page.getByRole('button', { name: '问题已解决，关闭工单' }).click();
  await b.page
    .getByRole('dialog', { name: '关闭工单' })
    .getByRole('button', { name: '确认关闭' })
    .click();
  await expect(b.page.getByTestId('ticket-status')).toHaveText('已关闭');
  await expect(b.page.getByTestId('ticket-closed')).toContainText('你已关闭此工单');
  await expect(queueRow(admin.page, own)).toHaveCount(0, { timeout: 25000 });
  // Only A's first ticket (A added more to it) still waits.
  await expect(admin.page.getByTestId('console-ticket-badge')).toHaveText('1', { timeout: 25000 });
  await b.context.close();
  await admin.context.close();
});

test('T2-05: three open tickets disable "提交工单" with the reason', async ({ browser }) => {
  const a = await visitor(browser, fixture.emails.ticketa);
  // One is open (the first); two more reach the cap.
  for (const subject of ['第二个问题', '第三个问题'])
    expect(
      (await call(a.page, 'POST', '/me/tickets', { category: 'other', subject, body: '说明' }))
        .status,
    ).toBe(201);
  await a.page.goto('/tickets');
  await expect(a.page.getByTestId('ticket-row')).toHaveCount(3);
  await expect(a.page.getByRole('button', { name: '提交工单' })).toBeDisabled();
  await expect(a.page.getByTestId('submit-blocked')).toContainText('处理中的工单最多 3 张');
  await a.context.close();
});

test('T2-06: deletion date, the three-day notice, and expiry on both sides', async ({
  browser,
}) => {
  const b = await visitor(browser, fixture.emails.ticketb);
  const subject = '即将到期的工单';
  const id = await submitTicket(b.page, {
    category: /连接与节点/,
    subject,
    body: '晚上连接经常断开。',
  });
  await expect(b.page.getByTestId('ticket-expiring')).toHaveCount(0);
  await control(`ticket-expiry?id=${id}&seconds=${2 * 86400}`, 'POST');
  await b.page.reload();
  await expect(b.page.getByTestId('ticket-expiring')).toContainText('即将删除');
  await shot(b.page, 'ticket-expiring-1440');
  await b.page.goto('/tickets');
  await expect(b.page.getByTestId('ticket-row').filter({ hasText: subject })).toHaveCount(1);
  const admin = await visitor(browser, fixture.emails.ticketroot);
  await admin.page.goto('/admin/console/tickets?status=all&keyword=' + encodeURIComponent(subject));
  await expect(queueRow(admin.page, subject)).toHaveCount(1);

  // Expired: the one-second sweep deletes the rows; the open detail says so on its next refresh.
  await b.page.goto('/tickets?id=' + id);
  await expect(b.page.getByTestId('ticket-detail')).toBeVisible();
  await control(`ticket-expiry?id=${id}&seconds=-1`, 'POST');
  await expect
    .poll(async () => (await control('ticket?id=' + id)).exists, { timeout: 15000 })
    .toBe(false);
  await expect(b.page.getByTestId('ticket-gone')).toContainText('该工单已超过保留期限并被删除', {
    timeout: 30000,
  });
  await b.page.goto('/tickets');
  await expect(b.page.getByTestId('tickets-empty')).toBeVisible();
  await admin.page.reload();
  await expect(admin.page.getByTestId('console-tickets-empty')).toBeVisible();
  await b.context.close();
  await admin.context.close();
});

test('T2-07: an administrator looking at the account reads tickets only and marks nothing read', async ({
  browser,
}) => {
  const admin = await adminTickets(browser);
  // An unread reply on A's first ticket.
  await queueRow(admin.page, first).click();
  await drawer(admin.page).getByLabel('回复').fill('请问是哪个设备上出现的？');
  await drawer(admin.page).getByRole('button', { name: '发送回复' }).click();
  await expect(drawer(admin.page).getByTestId('console-ticket-status')).toHaveText('已回复');

  await admin.page.goto('/admin/console/users?q=' + encodeURIComponent(fixture.emails.ticketa));
  await admin.page.getByRole('button', { name: `查看 ${fixture.emails.ticketa}` }).click();
  await admin.page
    .getByRole('dialog', { name: /^用户详情/ })
    .getByRole('button', { name: '模拟登录' })
    .click();
  const start = admin.page.getByRole('dialog', { name: /^模拟登录/ });
  await start.getByLabel('管理员密码').fill(fixture.password);
  await start.getByRole('button', { name: '开始模拟' }).click();
  await expect(admin.page).toHaveURL(/\/dashboard$/);
  await expect(admin.page.getByTestId('impersonation-banner')).toBeVisible();
  const writes: string[] = [];
  admin.page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().includes('/me/tickets'))
      writes.push(new URL(request.url()).pathname);
  });
  await nav(admin.page)
    .getByRole('link', { name: /我的工单/ })
    .click();
  await expect(unreadBadge(admin.page)).toHaveText('1');
  await expect(admin.page.getByTestId('ticket-row')).toHaveCount(3);
  await expect(admin.page.getByRole('button', { name: '提交工单' })).toHaveCount(0);
  await admin.page.getByTestId('ticket-row').filter({ hasText: first }).getByRole('link').click();
  await expect(admin.page.getByTestId('ticket-message').last()).toContainText('哪个设备');
  await expect(admin.page.getByTestId('ticket-reply')).toHaveCount(0);
  await expect(admin.page.getByRole('button', { name: /关闭工单/ })).toHaveCount(0);
  await admin.page.waitForTimeout(2000);
  expect(writes).toEqual([]);
  await expect(unreadBadge(admin.page)).toHaveText('1');
  await shot(admin.page, 'ticket-impersonation-1440');
  await admin.page
    .getByTestId('impersonation-banner')
    .getByRole('button', { name: '返回管理员' })
    .click();
  await expect(admin.page).toHaveURL(/\/admin\/console\/users$/);
  await admin.context.close();
});

test('T2-08: an older backend hides tickets; switched off they are read-only', async ({
  browser,
}) => {
  const signedIn = await visitor(browser, fixture.emails.ticketa);
  let flag: 'absent' | false = 'absent';
  await signedIn.context.route('**/api/v1/meta', async (route) => {
    const response = await route.fetch();
    const meta = (await response.json()) as Record<string, unknown>;
    if (flag === 'absent') delete meta.tickets_enabled;
    else meta.tickets_enabled = false;
    await route.fulfill({ response, json: meta });
  });
  // A fresh page that only ever saw the older /meta.
  await signedIn.page.close();
  const a = { context: signedIn.context, page: await signedIn.context.newPage() };
  const summaries: string[] = [];
  a.page.on('request', (request) => {
    if (request.url().includes('/tickets/summary')) summaries.push(request.url());
  });
  await a.page.goto('/dashboard');
  await expect(nav(a.page).getByRole('link', { name: '总览' })).toBeVisible();
  await expect(nav(a.page).getByRole('link', { name: /我的工单/ })).toHaveCount(0);
  await a.page.goto('/tickets');
  await expect(a.page.getByTestId('tickets-unavailable')).toContainText('工单服务暂未开放');
  await a.page.waitForTimeout(1500);
  expect(summaries).toEqual([]);

  flag = false;
  await a.page.goto('/tickets');
  await expect(a.page.getByTestId('tickets-paused')).toBeVisible();
  await expect(a.page.getByTestId('ticket-row')).toHaveCount(3);
  await expect(a.page.getByRole('button', { name: '提交工单' })).toHaveCount(0);
  await expect(nav(a.page).getByRole('link', { name: /我的工单/ })).toHaveCount(0);
  await a.page.getByTestId('ticket-row').first().getByRole('link').click();
  await expect(a.page.getByTestId('ticket-detail')).toBeVisible();
  await expect(a.page.getByTestId('ticket-reply')).toHaveCount(0);
  await a.context.close();

  // The console on an older backend.
  const admin = await visitor(browser, fixture.emails.ticketroot);
  await admin.context.route('**/api/v1/meta', async (route) => {
    const response = await route.fetch();
    const meta = (await response.json()) as Record<string, unknown>;
    delete meta.tickets_enabled;
    await route.fulfill({ response, json: meta });
  });
  await admin.page.goto('/admin/console/tickets');
  await expect(admin.page.getByTestId('console-tickets-unsupported')).toContainText(
    '当前版本暂不支持工单',
  );
  await expect(admin.page.getByTestId('console-ticket-badge')).toHaveCount(0);
  await admin.context.close();
});

test('T2-09: narrow screens show cards, dark theme, and the copy guard', async ({ browser }) => {
  const narrow = await visitor(browser, fixture.emails.ticketa, { width: 375, height: 812 });
  await narrow.page.goto('/tickets');
  await expect(narrow.page.getByTestId('ticket-cards')).toBeVisible();
  await expect(narrow.page.getByRole('table')).toHaveCount(0);
  const text = await narrow.page.evaluate(() => document.body.innerText);
  expect(bannedIn(text)).toEqual([]);
  expect(text).not.toContain('管理员');
  await shot(narrow.page, 'tickets-list-375', true);
  await narrow.page.getByTestId('ticket-row').filter({ hasText: first }).click();
  await expect(narrow.page.getByTestId('ticket-detail')).toBeVisible();
  await shot(narrow.page, 'ticket-detail-375', true);
  await narrow.context.close();

  const dark = await browser.newContext({
    colorScheme: 'dark',
    viewport: { width: 1440, height: 1000 },
  });
  const page = await dark.newPage();
  await signIn(page, fixture.emails.ticketa);
  await page.goto('/tickets');
  await expect(page.locator('html')).toHaveAttribute('data-mantine-color-scheme', 'dark');
  await expect(page.getByTestId('ticket-row').first()).toBeVisible();
  await shot(page, 'tickets-list-dark');
  await page.getByTestId('ticket-row').filter({ hasText: first }).getByRole('link').click();
  await expect(page.getByTestId('ticket-detail')).toBeVisible();
  await shot(page, 'ticket-detail-dark', true);
  await dark.close();

  const darkAdmin = await browser.newContext({
    colorScheme: 'dark',
    viewport: { width: 1440, height: 1000 },
  });
  const adminPage = await darkAdmin.newPage();
  await signIn(adminPage, fixture.emails.ticketroot);
  await adminPage.goto('/admin/console/tickets?status=all');
  await queueRow(adminPage, first).click();
  await expect(drawer(adminPage)).toBeVisible();
  await shot(adminPage, 'console-ticket-drawer-dark');
  await darkAdmin.close();

  const admin375 = await visitor(browser, fixture.emails.ticketroot, { width: 375, height: 812 });
  await admin375.page.goto('/admin/console/tickets?status=all');
  await expect(queueRow(admin375.page, first)).toHaveCount(1);
  await shot(admin375.page, 'console-tickets-375', true);
  await admin375.context.close();
});

// Round 3 (T3-06): the user's own check list U-36 … U-40 (handoff_ticket.md 16.2), walked step by
// step the way docs/ticket-operations.md describes it, with ticketc and real polling only.
test('T3-06: the check list U-36 … U-40 walked locally', async ({ browser }) => {
  const diagnostics = [
    'AsterLink 诊断信息',
    '时间: 2026-11-01 21:05',
    '应用版本: 1.0.0 (100)',
    '系统版本: macOS 15.1',
    '连接状态: 连接失败',
    '错误码: tunnel_failed',
  ].join('\n');
  const c = await visitor(browser, fixture.emails.ticketc);
  const admin = await adminTickets(browser);

  // U-36: a connection ticket with the copied diagnostics; number and a deletion date 30 days on.
  // Subjects of their own: the console search must find only this walk's tickets.
  const subject = 'U-36 东京线路连接失败';
  const id = await submitTicket(c.page, { category: /连接与节点/, subject, body: diagnostics });
  await expect(c.page.getByTestId('ticket-no')).toHaveText(/^工单号 T\d{12}$/);
  const pasted = c.page.locator('.ticket-text').first();
  await expect(pasted).toContainText('AsterLink 诊断信息');
  await expect(pasted).toContainText('错误码: tunnel_failed');
  expect(await pasted.evaluate((node) => (node as HTMLElement).innerText.trim())).toBe(diagnostics);
  const detail = (await call(c.page, 'GET', '/me/tickets/' + id)).json.ticket as {
    created_at: string;
    expires_at: string;
  };
  expect(Date.parse(detail.expires_at) - Date.parse(detail.created_at)).toBe(30 * 86400 * 1000);
  const shown = await c.page.evaluate((at) => {
    const d = new Date(at);
    const p = (n: number) => String(n).padStart(2, '0');
    return `将于 ${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())} 自动删除`;
  }, detail.expires_at);
  await expect(c.page.getByTestId('ticket-deletion')).toHaveText(shown);

  // U-37: the console page, never reloaded, shows it within one 15-second refresh.
  await expect(queueRow(admin.page, subject)).toHaveCount(1, { timeout: 25000 });
  await expect(admin.page.getByTestId('console-ticket-badge')).toBeVisible();

  // U-38: support replies while the user stays on another page; the badge comes within a minute.
  await c.page.goto('/orders');
  await expect(unreadBadge(c.page)).toHaveCount(0);
  await queueRow(admin.page, subject).click();
  await drawer(admin.page).getByLabel('回复').fill('已切换备用线路，请重新连接后再试。');
  await drawer(admin.page).getByRole('button', { name: '发送回复' }).click();
  await expect(drawer(admin.page).getByTestId('console-ticket-status')).toHaveText('已回复');
  await expect(unreadBadge(c.page)).toHaveText('1', { timeout: 75000 });
  await nav(c.page)
    .getByRole('link', { name: /我的工单/ })
    .click();
  await c.page.getByTestId('ticket-row').filter({ hasText: subject }).getByRole('link').click();
  await expect(
    c.page.getByTestId('ticket-message').filter({ hasText: '已切换备用线路' }),
  ).toContainText('客服');
  await expect(unreadBadge(c.page)).toHaveCount(0);

  // U-39: another ticket closed without a reply.
  const other = 'U-39 Windows 版安装后无法启动';
  const second = await submitTicket(c.page, {
    category: /客户端下载与安装/,
    subject: other,
    body: diagnostics,
  });
  await admin.page.goto('/admin/console/tickets');
  await expect(queueRow(admin.page, other)).toHaveCount(1, { timeout: 25000 });
  await queueRow(admin.page, other).click();
  await drawer(admin.page).getByRole('button', { name: '关闭工单' }).click();
  await admin.page
    .getByRole('dialog', { name: /^关闭工单/ })
    .getByRole('button', { name: '确认关闭' })
    .click();
  await expect(drawer(admin.page).getByTestId('console-ticket-status')).toHaveText('已关闭');
  await c.page.goto('/tickets?id=' + second);
  await expect(c.page.getByTestId('ticket-status')).toHaveText('已关闭（由客服关闭）');
  await expect(c.page.getByTestId('ticket-reply')).toHaveCount(0);

  // U-40: 30 days later (the deletion time moved to now), gone from both sides.
  for (const ticket of [id, second]) {
    await control(`ticket-expiry?id=${ticket}&seconds=-1`, 'POST');
    await expect
      .poll(async () => (await control('ticket?id=' + ticket)).exists, { timeout: 15000 })
      .toBe(false);
  }
  await c.page.goto('/tickets');
  await expect(c.page.getByTestId('tickets-empty')).toBeVisible();
  await c.page.locator('.mantine-SegmentedControl-label', { hasText: '已关闭' }).click();
  await expect(c.page.getByTestId('tickets-empty')).toBeVisible();
  await admin.page.goto('/admin/console/tickets?status=all&keyword=' + encodeURIComponent(subject));
  await expect(admin.page.getByTestId('console-tickets-empty')).toBeVisible();
  await admin.page.goto('/admin/console/tickets?status=all&keyword=' + encodeURIComponent(other));
  await expect(admin.page.getByTestId('console-tickets-empty')).toBeVisible();
  await c.context.close();
  await admin.context.close();
});
