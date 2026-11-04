import { expect, test, type Browser, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import { bannedIn } from './copy-words';
import { overflowing } from './layout';

// The usage guide (handoff_guide.md 8.3, round 1): the sidebar entry, public access, platform
// deep links and device detection, the download availability, layout and copy. The fixture has
// Android released and iPhone not (scripts/test-payment-web.py). homea only signs in and
// themeroot is an administrator; neither is changed here.
type Fixture = {
  api: string;
  password: string;
  emails: { homea: string; themeroot: string };
};

let fixture: Fixture;
test.beforeAll(async () => {
  const filename = process.env.PAYMENT_FIXTURE;
  if (!filename?.startsWith('/tmp/vpn-payment-'))
    throw new Error('Private payment fixture required');
  fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
});

const agents = {
  windows:
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36',
  macos:
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15',
  android:
    'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Mobile Safari/537.36',
  ios: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1',
} as const;

async function shot(page: Page, name: string) {
  const dir = `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${name}.png`, animations: 'disabled', fullPage: true });
}

async function login(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

const navbar = (page: Page) => page.locator('.mantine-AppShell-navbar');
const platform = (page: Page) => page.locator('[data-testid="guide-platform"]:visible');
const labels = (page: Page) => navbar(page).locator('a.nav-link').allInnerTexts();

async function agentPage(browser: Browser, userAgent: string) {
  const context = await browser.newContext({ userAgent });
  return { context, page: await context.newPage() };
}

test('the entry sits right under 下载客户端, for users and administrators, and lights up', async ({
  page,
}) => {
  for (const email of [fixture.emails.homea, fixture.emails.themeroot]) {
    await login(page, email);
    const entries = (await labels(page)).map((text) => text.trim());
    const at = entries.indexOf('下载客户端');
    expect(at).toBeGreaterThan(-1);
    expect(entries[at + 1]).toBe('使用教程');
    expect(entries[at + 2]).toBe('账号设置');
    await navbar(page).getByRole('link', { name: '使用教程', exact: true }).click();
    await expect(page).toHaveURL(/\/guide$/);
    await expect(navbar(page).locator('a.nav-link.active')).toHaveText('使用教程');
    await expect(page.getByRole('heading', { level: 1, name: '使用教程' })).toBeVisible();
    await page.context().clearCookies();
  }
});

test('a visitor who is signed out can read it, with the sidebar and without a redirect', async ({
  page,
}) => {
  await page.goto('/guide');
  await expect(page).toHaveURL(/\/guide$/);
  await expect(page).toHaveTitle(/^使用教程 · /);
  await expect(page.getByRole('heading', { level: 1, name: '使用教程' })).toBeVisible();
  const entries = (await labels(page)).map((text) => text.trim());
  expect(entries).toContain('使用教程');
  await expect(navbar(page).locator('a.nav-link.active')).toHaveText('使用教程');
  await expect(page.getByTestId('guide-before')).toHaveCount(4);
  // Signed out: the ticket button is still offered (it asks for sign-in first), the chat is not.
  await expect(
    page.getByTestId('guide-help').getByRole('link', { name: '提交工单' }),
  ).toBeVisible();
  await expect(
    page.getByTestId('guide-help').getByRole('button', { name: '在线客服' }),
  ).toHaveCount(0);
});

test('the platform comes from the link, switches in place, survives a reload and falls back', async ({
  browser,
}) => {
  const { context, page } = await agentPage(browser, agents.windows);
  await page.goto('/guide?platform=ios');
  await expect(platform(page)).toHaveAttribute('data-platform', 'ios');
  await expect(page.getByRole('heading', { level: 3, name: '用 Safari 打开下载页' })).toBeVisible();
  const history = await page.evaluate(() => window.history.length);

  await page.getByText('Android', { exact: true }).click();
  await expect(page).toHaveURL(/\/guide\?platform=android$/);
  await expect(platform(page)).toHaveAttribute('data-platform', 'android');
  // Only the chosen platform is rendered.
  await expect(page.getByText('用 Safari 打开下载页')).toHaveCount(0);
  expect(await page.evaluate(() => window.history.length)).toBe(history);
  await page.reload();
  await expect(platform(page)).toHaveAttribute('data-platform', 'android');

  for (const [label, value] of [
    ['Windows', 'windows'],
    ['macOS', 'macos'],
    ['iPhone', 'ios'],
  ] as const) {
    await page.getByText(label, { exact: true }).click();
    await expect(platform(page)).toHaveAttribute('data-platform', value);
  }

  // An unknown value is ignored: this browser is a Windows one.
  await page.goto('/guide?platform=linux');
  await expect(platform(page)).toHaveAttribute('data-platform', 'windows');
  await context.close();
});

for (const [name, agent] of Object.entries(agents))
  test(`a ${name} browser opens its own guide`, async ({ browser }) => {
    const { context, page } = await agentPage(browser, agent);
    await page.goto('/guide');
    await expect(platform(page)).toHaveAttribute('data-platform', name);
    await context.close();
  });

test('every platform has the full outline; the package hint follows the release', async ({
  browser,
}) => {
  const { context, page } = await agentPage(browser, agents.windows);
  const outline = [
    '适用设备',
    '第一步 下载安装',
    '第二步 首次授权',
    '第三步 登录',
    '第四步 选择线路',
    '第五步 连接与断开',
    '第六步 查看套餐与用量',
    '升级与卸载',
    '常见问题',
    '仍未解决',
  ];
  for (const id of ['windows', 'macos', 'android', 'ios']) {
    await page.goto(`/guide?platform=${id}`);
    await expect(platform(page)).toHaveAttribute('data-platform', id);
    for (const title of outline)
      await expect(
        page.getByRole('heading', { name: new RegExp(title) }).first(),
        `${id}: ${title}`,
      ).toBeVisible();
    await expect(
      page.getByTestId('guide-faq').locator('.mantine-Accordion-item').first(),
    ).toBeVisible();
    // Android is released on this fixture; iPhone and the desktop clients are not.
    const download = page.getByTestId('guide-download');
    await expect(download).toHaveCount(1);
    if (id === 'android') {
      await expect(download).toHaveAttribute('data-available', 'true');
      await expect(download.getByRole('link', { name: '前往下载' })).toHaveAttribute(
        'href',
        '/download#android',
      );
    } else {
      await expect(download).toHaveAttribute('data-available', 'false');
      await expect(download).toContainText('联系客服获取');
      await expect(page.getByRole('link', { name: '前往下载' })).toHaveCount(0);
    }
  }
  // An answer opens on the keyboard.
  const first = page.getByTestId('guide-faq').getByRole('button').first();
  await first.focus();
  await page.keyboard.press('Enter');
  await expect(first).toHaveAttribute('aria-expanded', 'true');
  await context.close();
});

test('the download page links to the matching guide and keeps its anchors', async ({ page }) => {
  await page.goto('/download');
  const links = page.getByTestId('download-guide-link');
  await expect(links).toHaveCount(2);
  await expect(links.nth(0)).toHaveAttribute('href', '/guide?platform=android');
  await expect(links.nth(1)).toHaveAttribute('href', '/guide?platform=ios');
  await expect(page.locator('#android')).toHaveCount(1);
  await expect(page.locator('#ios')).toHaveCount(1);
  await links.nth(1).click();
  await expect(platform(page)).toHaveAttribute('data-platform', 'ios');
});

test('the signed-in help block offers tickets, and no page shows development wording', async ({
  page,
}) => {
  await login(page, fixture.emails.homea);
  await page.goto('/guide?platform=android');
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  const text = await page.evaluate(() => document.body.innerText);
  const words = bannedIn(text);
  expect(words, `shows ${words.join(', ')}`).toEqual([]);
  await expect(
    page.getByTestId('guide-help').getByRole('link', { name: '提交工单' }),
  ).toHaveAttribute('href', '/tickets');
});

test('no horizontal overflow at phone and desktop width; both themes are kept as evidence', async ({
  browser,
}) => {
  for (const scheme of ['light', 'dark'] as const) {
    const context = await browser.newContext({ colorScheme: scheme, userAgent: agents.windows });
    const page = await context.newPage();
    for (const [width, height] of [
      [390, 844],
      [1280, 900],
    ]) {
      await page.setViewportSize({ width, height });
      for (const id of ['windows', 'macos', 'android', 'ios']) {
        await page.goto(`/guide?platform=${id}`);
        await expect(platform(page)).toHaveAttribute('data-platform', id);
        expect(await overflowing(page), `${id} ${width}px ${scheme}`).toEqual([]);
        // The four device names all sit inside the viewport (the control does not scroll).
        for (const box of await page
          .locator('.guide-seg label')
          .evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().right)))
          expect(box, `${id} ${width}px device switch`).toBeLessThanOrEqual(width);
        if (id === 'windows' || id === 'ios') await shot(page, `guide-${id}-${width}-${scheme}`);
      }
    }
    await context.close();
  }
});
