import { expect, test, type Browser, type Page } from '@playwright/test';
import { readFile } from 'node:fs/promises';

// Mixed deployment of support tickets (handoff_ticket.md 14.1, round 3: T3-01, T3-02), the half
// that is the new website. scripts/test-release-rehearsal.py starts this website against a real
// API binary twice: the release before tickets (75f906d, no tickets_enabled in /meta, no ticket
// routes) while the data already holds tickets, and the current binary after the upgrade back,
// where the tickets made before the rollback must still be there. Accounts and the expected
// ticket numbers come from the script's private file; each run tests one backend.
type Account = { email: string; password: string };
type Compat = {
  backend: 'old' | 'current';
  user: Account;
  admin: Account;
  // current only: what the user and the console must show.
  user_tickets?: string[];
  gone_tickets?: string[];
  unread?: number;
  open_count?: number;
};

let compat: Compat;
test.beforeAll(async () => {
  const filename = process.env.REHEARSAL_TICKET_COMPAT;
  if (!filename?.startsWith('/tmp/vpn-release-'))
    throw new Error('Private rehearsal ticket file required');
  compat = JSON.parse(await readFile(filename, 'utf8')) as Compat;
});

test.describe.configure({ mode: 'serial', timeout: 120_000 });

// Every request to a ticket route and every uncaught error of the page.
function watch(page: Page) {
  const ticketCalls: string[] = [];
  const errors: string[] = [];
  page.on('request', (request) => {
    if (/\/api\/v1\/(me|admin\/console)\/tickets/.test(request.url()))
      ticketCalls.push(request.url());
  });
  page.on('pageerror', (error) => errors.push(error.message));
  return { ticketCalls, errors };
}

async function signIn(browser: Browser, account: Account) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  const seen = watch(page);
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(account.email);
  await page.locator('input[type="password"]').fill(account.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  return { context, page, seen };
}

const nav = (page: Page) => page.getByRole('navigation').first();

test('the new website against the release before tickets', async ({ browser }) => {
  test.skip(compat.backend !== 'old', 'this run tests the current backend');
  const user = await signIn(browser, compat.user);
  await expect(nav(user.page).getByRole('link', { name: '推荐有礼' })).toBeVisible();
  await expect(nav(user.page).getByRole('link', { name: '我的工单' })).toHaveCount(0);
  for (const path of ['/orders', '/plans', '/dashboard']) {
    await user.page.goto(path);
    await expect(nav(user.page).getByRole('link', { name: '下载客户端' })).toBeVisible();
    await expect(nav(user.page).getByRole('link', { name: '我的工单' })).toHaveCount(0);
  }
  // Typed in by hand: the page explains, and still asks nothing.
  await user.page.goto('/tickets');
  await expect(user.page.getByTestId('tickets-unavailable')).toBeVisible();
  await expect(user.page.getByRole('button', { name: '提交工单' })).toHaveCount(0);
  await user.page.goto('/tickets?id=00000000-0000-4000-8000-000000000000');
  await expect(user.page.getByTestId('tickets-unavailable')).toBeVisible();
  expect(user.seen.ticketCalls).toEqual([]);
  expect(user.seen.errors).toEqual([]);
  await user.context.close();

  const admin = await signIn(browser, compat.admin);
  await admin.page.goto('/admin/console/users');
  await expect(admin.page.getByRole('heading', { name: '管理后台' })).toBeVisible();
  await expect(admin.page.getByTestId('console-ticket-badge')).toHaveCount(0);
  await expect(admin.page.getByTestId('console-ticket-dot')).toHaveCount(0);
  await admin.page.goto('/admin/console/tickets');
  await expect(admin.page.getByTestId('console-tickets-unsupported')).toBeVisible();
  // Some time on the page: no 15-second poll starts against the old API.
  await admin.page.waitForTimeout(17_000);
  expect(admin.seen.ticketCalls).toEqual([]);
  expect(admin.seen.errors).toEqual([]);
  await admin.context.close();
});

test('the new website against the current backend after the upgrade back', async ({ browser }) => {
  test.skip(compat.backend !== 'current', 'this run tests the release before tickets');
  const user = await signIn(browser, compat.user);
  const entry = nav(user.page).getByRole('link', { name: /^我的工单/ });
  await expect(entry).toBeVisible();
  await expect(user.page.getByTestId('ticket-unread-badge')).toHaveText(String(compat.unread));
  await expect(entry).toHaveAttribute('href', '/tickets');
  // By address: this account has an announcement to acknowledge, whose dialog covers the page.
  await user.page.goto('/tickets');
  const rows = user.page.getByTestId('ticket-row');
  await expect(rows).toHaveCount(compat.user_tickets!.length);
  for (const number of compat.user_tickets!)
    await expect(rows.filter({ hasText: number })).toHaveCount(1);
  for (const number of compat.gone_tickets!)
    await expect(user.page.getByText(number)).toHaveCount(0);
  expect(user.seen.errors).toEqual([]);
  await user.context.close();

  const admin = await signIn(browser, compat.admin);
  await admin.page.goto('/admin/console/tickets');
  await expect(admin.page.getByTestId('console-ticket-badge')).toHaveText(
    String(compat.open_count),
  );
  await expect(admin.page.getByTestId('console-ticket-row')).toHaveCount(compat.open_count!);
  for (const number of compat.gone_tickets!)
    await expect(admin.page.getByText(number)).toHaveCount(0);
  expect(admin.seen.errors).toEqual([]);
  await admin.context.close();
});
