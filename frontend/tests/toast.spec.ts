import { expect, test, type Browser, type Locator, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';

// Browser acceptance for the short messages (handoff_message.md 8, M1-01 … M1-15). It runs against
// the real API on a private PostgreSQL, started by scripts/test-payment-web.py. The paths through
// a full reload with the support chat loaded are in support-widget.spec.ts.
type Fixture = {
  password: string;
  emails: Record<'toastroot' | 'toasta' | 'toastb' | 'toastdel', string>;
};

let fixture: Fixture;
test.beforeAll(async () => {
  const filename = process.env.PAYMENT_FIXTURE;
  if (!filename?.startsWith('/tmp/vpn-payment-'))
    throw new Error('Private payment fixture required');
  fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
});

async function shot(page: Page, name: string) {
  const dir = `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${name}.png`, animations: 'disabled' });
}

const toasts = (page: Page) => page.getByTestId('toast');
const toast = (page: Page, text: string) => toasts(page).filter({ hasText: text });
const dialogs = (page: Page) => page.getByRole('dialog');
const storageKey = 'asterlink-next-toast';

async function acknowledgeAll(page: Page) {
  const shown = () =>
    dialogs(page)
      .waitFor({ state: 'visible', timeout: 3000 })
      .then(() => true)
      .catch(() => false);
  while (await shown()) {
    await page.getByRole('button', { name: '知道了' }).click();
    await expect(page.getByRole('button', { name: '知道了' }))
      .toBeHidden({ timeout: 3000 })
      .catch(() => undefined);
  }
}

async function login(page: Page, email: string, path = '/login') {
  await page.goto(path);
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
}

async function visitor(browser: Browser, email: string, width = 1440) {
  const context = await browser.newContext({
    baseURL: process.env.PLAYWRIGHT_BASE_URL,
    viewport: { width, height: 900 },
  });
  const page = await context.newPage();
  await login(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  return { context, page };
}

// Shown, then gone by itself within its duration (plus the exit transition and some slack).
async function goesAway(message: Locator, durationMs: number) {
  await expect(message).toBeVisible();
  const shownAt = Date.now();
  await expect(message).toHaveCount(0, { timeout: durationMs + 3000 });
  expect(Date.now() - shownAt).toBeGreaterThan(durationMs - 1500);
}

// WCAG contrast of an element's text against the message card behind it.
async function contrast(element: Locator) {
  return element.evaluate((node) => {
    const rgb = (value: string) => (value.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
    const luminance = ([r, g, b]: number[]) => {
      const lin = (c: number) => {
        const v = c / 255;
        return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
    };
    const card = node.closest('[data-testid="toast"]')!;
    const fg = luminance(rgb(getComputedStyle(node).color));
    const bg = luminance(rgb(getComputedStyle(card).backgroundColor));
    return (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05);
  });
}

test('signing in and out: one message each, polite, gone by itself, paused while hovered', async ({
  page,
}) => {
  const email = fixture.emails.toasta;
  // M1-01, M1-10: a polite status message that names the person, nothing assertive.
  await login(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  const welcome = toast(page, '登录成功');
  await expect(welcome).toBeVisible();
  await expect(welcome).toContainText('欢迎回来，Pay toasta');
  await expect(welcome).toHaveAttribute('role', 'status');
  await expect(welcome).toHaveAttribute('data-tone', 'success');
  await expect(page.getByRole('alert').filter({ hasText: '登录成功' })).toHaveCount(0);
  await shot(page, 'toast-signed-in');
  await goesAway(welcome, 3000);
  await acknowledgeAll(page);

  // M1-15: refreshing, by the button or by returning to the window, says nothing.
  await page.getByRole('button', { name: '刷新' }).click();
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await page.waitForTimeout(1500);
  await expect(toasts(page)).toHaveCount(0);

  // M1-04: signing out (no support chat loaded) shows its message once, on the sign-in page.
  await page.getByRole('button', { name: '账户菜单' }).click();
  await page.getByRole('menuitem', { name: '退出登录' }).click();
  await expect(page).toHaveURL(/\/login$/);
  await expect(toast(page, '已退出登录')).toHaveCount(1);
  await expect(toast(page, '已退出登录')).toHaveAttribute('role', 'status');
  expect(await page.evaluate((key) => sessionStorage.getItem(key), storageKey)).toBeNull();
  await goesAway(toast(page, '已退出登录'), 3000);
  await page.reload();
  await page.waitForTimeout(1000);
  await expect(toasts(page)).toHaveCount(0);

  // M1-01: with a return address (only a payment result is honoured) the message follows the
  // person there.
  const result = '/pay/result?order=00000000-0000-4000-8000-000000000000';
  await login(page, email, '/login?next=' + encodeURIComponent(result));
  await expect(page).toHaveURL((url) => url.pathname + url.search === result);
  const again = toast(page, '登录成功');
  await expect(again).toBeVisible();

  // M1-11: hovering keeps it; leaving starts the countdown again.
  await again.hover();
  await page.waitForTimeout(4500);
  await expect(again).toBeVisible();
  await page.mouse.move(5, 700);
  await goesAway(again, 3000);

  // The close button ends it at once.
  await page.getByRole('button', { name: '账户菜单' }).click();
  await page.getByRole('menuitem', { name: '退出登录' }).click();
  await expect(toast(page, '已退出登录')).toBeVisible();
  await toast(page, '已退出登录').locator('button').click();
  await expect(toasts(page)).toHaveCount(0, { timeout: 2000 });
});

test('a phone screen and both appearances: the message fits and reads clearly', async ({
  browser,
}) => {
  for (const scheme of ['light', 'dark'] as const) {
    const context = await browser.newContext({
      baseURL: process.env.PLAYWRIGHT_BASE_URL,
      viewport: { width: 375, height: 812 },
      colorScheme: scheme,
    });
    const page = await context.newPage();
    await login(page, fixture.emails.toasta);
    await expect(page).toHaveURL(/\/dashboard$/);
    const welcome = toast(page, '登录成功');
    await expect(welcome).toBeVisible();
    // M1-12: inside the screen with a margin on both sides, once it has slid in.
    await page.waitForTimeout(400);
    const box = (await welcome.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(8);
    expect(box.x + box.width).toBeLessThanOrEqual(375 - 8);
    expect(box.y).toBeGreaterThanOrEqual(0);
    // M1-13: title and text reach 4.5:1 on the card.
    expect(await contrast(welcome.locator('.mantine-Notification-title'))).toBeGreaterThanOrEqual(
      4.5,
    );
    expect(
      await contrast(welcome.locator('.mantine-Notification-description')),
    ).toBeGreaterThanOrEqual(4.5);
    await shot(page, `toast-375-${scheme}`);
    await context.close();
  }
});

test('the session ended elsewhere: one warning, then the sign-in page', async ({ browser }) => {
  // M1-05, M1-06: another browser changes the password, which ends every other session.
  const email = fixture.emails.toastb;
  const first = await visitor(browser, email);
  await acknowledgeAll(first.page);
  const second = await visitor(browser, email);
  await acknowledgeAll(second.page);
  await second.page.goto('/account');
  const form = second.page.locator('main');
  const next = 'Toast-Changed-2026';
  await form.getByLabel('当前密码', { exact: true }).fill(fixture.password);
  await form.getByLabel('新密码', { exact: true }).fill(next);
  await form.getByLabel('确认新密码', { exact: true }).fill(next);
  await form.getByRole('button', { name: '修改密码' }).click();
  const changed = toast(second.page, '密码已修改');
  await expect(changed).toContainText('其他设备上的登录已退出');
  // The result is the message alone: nothing stays behind on the card.
  await expect(form.getByText('密码已修改')).toHaveCount(0);
  await goesAway(changed, 3000);
  await expect(form.getByText('密码已修改')).toHaveCount(0);
  await second.context.close();

  // The first browser learns it on its next request: several at once, one message.
  await first.page.getByRole('navigation').getByRole('link', { name: '节点列表' }).click();
  await expect(first.page).toHaveURL(/\/login$/);
  const ended = toast(first.page, '登录已失效');
  await expect(ended).toHaveCount(1);
  await expect(ended).toContainText('请重新登录');
  await expect(ended).toHaveAttribute('data-tone', 'warning');
  await shot(first.page, 'toast-session-ended');
  // Signing in again is a fresh start: no stale warning.
  await first.page.getByLabel('邮箱地址').fill(email);
  await first.page.locator('input[type="password"]').fill(next);
  await first.page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(first.page).toHaveURL(/\/dashboard$/);
  await expect(toast(first.page, '登录成功')).toBeVisible();
  await expect(toast(first.page, '登录已失效')).toHaveCount(0);
  await first.context.close();
});

test('the account deleted: the home page says so once', async ({ page }) => {
  // M1-07: the page is replaced by a full load; the message waits for the home page.
  const email = fixture.emails.toastdel;
  await login(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  await acknowledgeAll(page);
  await page.goto('/account');
  await page.getByRole('main').getByRole('button', { name: '删除账号' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('当前密码').fill(fixture.password);
  await dialog.getByLabel(/请输入完整邮箱/).fill(email);
  const confirm = dialog.getByRole('button', { name: /^永久删除账号/ });
  await expect(confirm).toBeEnabled({ timeout: 15000 });
  await confirm.click();
  await expect(page).toHaveURL((url) => url.pathname === '/');
  const deleted = toast(page, '账号已删除');
  await expect(deleted).toHaveCount(1);
  await expect(deleted).toContainText('感谢你的使用');
  await shot(page, 'toast-account-deleted');
  expect(await page.evaluate((key) => sessionStorage.getItem(key), storageKey)).toBeNull();
  await page.reload();
  await page.waitForTimeout(1000);
  await expect(toasts(page)).toHaveCount(0);
});

test('back to the administrator: the console says so, with user-page wording', async ({
  browser,
}) => {
  // M1-08.
  const admin = await visitor(browser, fixture.emails.toastroot);
  await acknowledgeAll(admin.page);
  const target = fixture.emails.toasta;
  await admin.page.goto('/admin/console/users?q=' + encodeURIComponent(target));
  await admin.page.getByRole('button', { name: `查看 ${target}` }).click();
  await admin.page
    .getByRole('dialog', { name: /^用户详情/ })
    .getByRole('button', { name: '模拟登录' })
    .click();
  const start = admin.page.getByRole('dialog', { name: /^模拟登录/ });
  await start.getByLabel('管理员密码').fill(fixture.password);
  await start.getByRole('button', { name: '开始模拟' }).click();
  await expect(admin.page).toHaveURL(/\/dashboard$/);
  await admin.page
    .getByTestId('impersonation-banner')
    .getByRole('button', { name: '返回管理员' })
    .click();
  await expect(admin.page).toHaveURL(/\/admin\/console\/users$/);
  const back = toast(admin.page, '已返回管理员账号');
  await expect(back).toHaveCount(1);
  await expect(back).not.toContainText('模拟');
  await admin.context.close();
});

test('the next-page message: only a fresh, known key, shown once', async ({ page }) => {
  // M1-14 in the browser (the unit test covers every malformed value). The value is written from a
  // same-origin document that is not the app (the icon), then the app loads: an app page open
  // while writing could still be hydrating and take the value itself, which hid the message
  // from the next load on a busy CI runner.
  const arrive = async (value: string) => {
    await page.goto('/icon.svg');
    await page.evaluate(([key, v]) => sessionStorage.setItem(key, v), [storageKey, value] as const);
    await page.goto('/login');
  };
  const stored = () => page.evaluate((key) => sessionStorage.getItem(key), storageKey);
  await arrive(JSON.stringify({ key: 'signed-out', at: Date.now() }));
  await expect(toast(page, '已退出登录')).toHaveCount(1);
  expect(await stored()).toBeNull();
  await page.reload();
  await page.waitForTimeout(1000);
  await expect(toasts(page)).toHaveCount(0);
  for (const value of [
    JSON.stringify({ key: 'signed-out', at: Date.now() - 31_000 }),
    JSON.stringify({ key: '<b>领取奖励</b>', at: Date.now() }),
    '{broken',
  ]) {
    await arrive(value);
    // The page read and removed the value, and showed nothing for it.
    await expect.poll(stored).toBeNull();
    await page.waitForTimeout(1000);
    await expect(toasts(page)).toHaveCount(0);
  }
});
