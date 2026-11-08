import { expect, test, type Browser, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import { bannedIn } from './copy-words';
import { overflowing } from './layout';
import { platforms, type PlatformId } from '../src/lib/guides';
import { pageCopy } from '../src/lib/seo-copy';

const pageTitle = (id: PlatformId) => pageCopy[`/guide/${id}`].title;

// The usage guide (handoff_guide.md 8.3, rounds 1–2; handoff_seo.md D5, round 3): the sidebar
// entry, public access, the overview and one address per platform (S3-03, S3-04), the download
// availability, layout and copy, and the pictures (only the chosen platform's load, numbered
// marks, zoom). The fixture has
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
  // A full-page capture does not scroll, so lazy pictures below the fold would stay blank.
  await page.evaluate(() =>
    Promise.all(
      [...document.images].map((img) => {
        img.loading = 'eager';
        return img.decode().catch(() => undefined);
      }),
    ),
  );
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
  await expect(page.getByTestId('guide-hub-card')).toHaveCount(4);
  // A platform page keeps the sidebar entry lit (S3-04) and offers the help block.
  await page.getByRole('link', { name: '查看 Windows 教程' }).click();
  await expect(page).toHaveURL(/\/guide\/windows$/);
  await expect(navbar(page).locator('a.nav-link.active')).toHaveText('使用教程');
  await expect(page.getByRole('heading', { level: 1, name: 'Windows 使用教程' })).toBeVisible();
  // Signed out: the ticket button is still offered (it asks for sign-in first), the chat is not.
  await expect(
    page.getByTestId('guide-help').getByRole('link', { name: '提交工单' }),
  ).toBeVisible();
  await expect(
    page.getByTestId('guide-help').getByRole('button', { name: '在线客服' }),
  ).toHaveCount(0);
});

test('each platform has its own address; the switch is links, back and forward step through them', async ({
  browser,
}) => {
  const { context, page } = await agentPage(browser, agents.windows);
  await page.goto('/guide/ios');
  await expect(platform(page)).toHaveAttribute('data-platform', 'ios');
  await expect(page.getByRole('heading', { level: 3, name: '用 Safari 打开下载页' })).toBeVisible();
  const history = await page.evaluate(() => window.history.length);
  const switcher = page.getByRole('navigation', { name: '选择设备' });
  await expect(switcher.getByRole('link')).toHaveCount(4);
  await expect(switcher.locator('a[aria-current="page"]')).toHaveText('iPhone');

  // The switch keeps the reading position, as it did before.
  // 200 pixels below the top, clear of the fixed header, so the click itself needs no scrolling.
  await switcher.evaluate((node) =>
    window.scrollTo(0, node.getBoundingClientRect().top + window.scrollY - 200),
  );
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(100);
  const before = await page.evaluate(() => window.scrollY);
  await switcher.getByRole('link', { name: 'Android' }).click();
  await expect(page).toHaveURL(/\/guide\/android$/);
  await expect(platform(page)).toHaveAttribute('data-platform', 'android');
  expect(Math.abs((await page.evaluate(() => window.scrollY)) - before)).toBeLessThan(5);
  // Only the chosen platform is rendered.
  await expect(page.getByText('用 Safari 打开下载页')).toHaveCount(0);
  expect(await page.evaluate(() => window.history.length)).toBe(history + 1);
  await expect(switcher.locator('a[aria-current="page"]')).toHaveText('Android');
  await page.goBack();
  await expect(page).toHaveURL(/\/guide\/ios$/);
  await expect(platform(page)).toHaveAttribute('data-platform', 'ios');
  await page.goForward();
  await expect(page).toHaveURL(/\/guide\/android$/);
  await expect(platform(page)).toHaveAttribute('data-platform', 'android');
  await page.reload();
  await expect(platform(page)).toHaveAttribute('data-platform', 'android');

  for (const [label, value] of [
    ['Windows', 'windows'],
    ['macOS', 'macos'],
    ['iPhone', 'ios'],
  ] as const) {
    await switcher.getByRole('link', { name: label }).click();
    await expect(platform(page)).toHaveAttribute('data-platform', value);
    await expect(page).toHaveTitle(`${pageTitle(value)}`);
  }

  // The breadcrumb leads back to the overview; old addresses land on the new ones (S3-02).
  await page.getByTestId('guide-crumbs').getByRole('link', { name: '使用教程' }).click();
  await expect(page).toHaveURL(/\/guide$/);
  await page.goto('/guide?platform=macos');
  await expect(page).toHaveURL(/\/guide\/macos$/);
  await expect(platform(page)).toHaveAttribute('data-platform', 'macos');
  await page.goto('/guide?platform=linux');
  await expect(page).toHaveURL(/\/guide$/);
  await expect(page.getByTestId('guide-hub-card')).toHaveCount(4);
  await context.close();
});

for (const [name, agent] of Object.entries(agents))
  test(`a ${name} browser sees its own device recommended on the overview`, async ({ browser }) => {
    const { context, page } = await agentPage(browser, agent);
    await page.goto('/guide');
    const cards = page.getByTestId('guide-hub-card');
    await expect(cards.and(page.locator(`[data-platform="${name}"]`))).toHaveAttribute(
      'data-recommended',
      'true',
    );
    await expect(page.locator('[data-recommended="true"]')).toHaveCount(1);
    const next = page.getByTestId('guide-recommend').getByRole('link');
    await expect(next).toHaveAttribute('href', `/guide/${name}`);
    await next.click();
    await expect(page).toHaveURL(new RegExp(`/guide/${name}$`));
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
    await page.goto(`/guide/${id}`);
    await expect(platform(page)).toHaveAttribute('data-platform', id);
    for (const title of outline)
      await expect(
        page.getByRole('heading', { name: new RegExp(title) }).first(),
        `${id}: ${title}`,
      ).toBeVisible();
    await expect(
      page.getByTestId('guide-faq').locator('.mantine-Accordion-item').first(),
    ).toBeVisible();
    // Android is published on this fixture; iPhone and the desktop clients are not.
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
  // Windows, macOS, Android, iPhone each link to their guide; Linux has none yet (handoff_download.md D12).
  const links = page.getByTestId('download-guide-link');
  await expect(links).toHaveCount(4);
  for (const [i, id] of ['windows', 'macos', 'android', 'ios'].entries())
    await expect(links.nth(i)).toHaveAttribute('href', `/guide/${id}`);
  for (const id of ['windows', 'macos', 'android', 'ios', 'linux'])
    await expect(page.locator(`#${id}`)).toHaveCount(1);
  await links.nth(3).click();
  await expect(platform(page)).toHaveAttribute('data-platform', 'ios');
});

test('the signed-in help block offers tickets, and no page shows development wording', async ({
  page,
}) => {
  await login(page, fixture.emails.homea);
  await page.goto('/guide/android');
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
        await page.goto(`/guide/${id}`);
        await expect(platform(page)).toHaveAttribute('data-platform', id);
        expect(await overflowing(page), `${id} ${width}px ${scheme}`).toEqual([]);
        // The four device names all sit inside the viewport (the control does not scroll).
        for (const box of await page
          .locator('.guide-seg a')
          .evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().right)))
          expect(box, `${id} ${width}px device switch`).toBeLessThanOrEqual(width);
        // All four platforms, both widths, both themes are kept as evidence (G-18); no picture is broken (G-17).
        await shot(page, `guide-${id}-${width}-${scheme}`);
        const broken = await page.evaluate(() =>
          [...document.images]
            .filter((img) => !img.complete || img.naturalWidth === 0)
            .map((img) => img.src),
        );
        expect(broken, `${id} ${width}px ${scheme}: broken pictures`).toEqual([]);
      }
    }
    await context.close();
  }
});

// ---- pictures (round 2) -------------------------------------------------------------------------

const ids: PlatformId[] = ['windows', 'macos', 'android', 'ios'];
const shotCount = (id: PlatformId) =>
  platforms[id].sections.flatMap((section) => section.steps).filter((step) => step.shot).length;

test('each platform loads its own pictures and no other platform’s; every one decodes', async ({
  browser,
}) => {
  for (const id of ids) {
    const { context, page } = await agentPage(browser, agents.windows);
    const requested: string[] = [];
    page.on('request', (request) => {
      const path = new URL(request.url()).pathname;
      if (path.startsWith('/guide/') && path.endsWith('.webp')) requested.push(path);
    });
    await page.goto(`/guide/${id}`);
    await expect(platform(page)).toHaveAttribute('data-platform', id);
    const figures = page.getByTestId('guide-shot');
    await expect(figures).toHaveCount(shotCount(id));
    for (let i = 0; i < shotCount(id); i++) {
      const img = figures.nth(i).locator('img');
      await img.scrollIntoViewIfNeeded();
      await expect
        .poll(() => img.evaluate((node: HTMLImageElement) => node.complete && node.naturalWidth), {
          message: `${id} picture ${i + 1} decodes`,
        })
        .toBeGreaterThan(0);
      await expect(img).toHaveAttribute('alt', /.{8,}/);
      await expect(figures.nth(i)).toContainText('示意图，线路名称与数据以实际为准');
    }
    expect(requested.length, `${id} requested its pictures`).toBeGreaterThan(0);
    for (const path of requested)
      expect(path, `${id} page asked for ${path}`).toMatch(`/guide/${id}/`);
    // The marks are for the eye only; the step text names the same numbers.
    const marks = page.locator('.guide-mark');
    expect(await marks.count()).toBeGreaterThan(0);
    for (const hidden of await marks.evaluateAll((nodes) =>
      nodes.map((node) => node.getAttribute('aria-hidden')),
    ))
      expect(hidden).toBe('true');
    await context.close();
  }
});

test('a picture opens larger on click or Enter and closes on Escape', async ({ browser }) => {
  const { context, page } = await agentPage(browser, agents.windows);
  await page.goto('/guide/macos');
  const frame = page.locator('.guide-shot-frame').first();
  await frame.scrollIntoViewIfNeeded();
  await frame.click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('img')).toHaveAttribute('alt', /登录页/);
  await expect(dialog.locator('.guide-mark')).toHaveCount(1);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await frame.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button', { name: '关闭' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await context.close();
});

test('with reduced motion the marks and the zoom do not animate', async ({ browser }) => {
  const context = await browser.newContext({ reducedMotion: 'reduce', userAgent: agents.windows });
  const page = await context.newPage();
  await page.goto('/guide/android');
  const mark = page.locator('.guide-mark').first();
  await mark.scrollIntoViewIfNeeded();
  expect(await mark.evaluate((node) => getComputedStyle(node).animationName)).toBe('none');
  await page.locator('.guide-shot-frame').first().click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  expect(
    await dialog.evaluate(
      (node) =>
        node.getAnimations({ subtree: true }).filter((a) => a.playState === 'running').length,
    ),
  ).toBe(0);
  await context.close();
});

test('the marks sit on their buttons: one picture per platform kept as evidence', async ({
  browser,
}) => {
  for (const scheme of ['light', 'dark'] as const) {
    const context = await browser.newContext({ colorScheme: scheme, userAgent: agents.windows });
    const page = await context.newPage();
    await page.setViewportSize({ width: 1280, height: 900 });
    for (const id of ids) {
      await page.goto(`/guide/${id}`);
      const connect = page.locator('#connect .guide-step').first();
      await connect.scrollIntoViewIfNeeded();
      const img = connect.locator('img');
      await expect
        .poll(() => img.evaluate((node: HTMLImageElement) => node.naturalWidth))
        .toBeGreaterThan(0);
      await expect(connect.locator('.guide-mark')).not.toHaveCount(0);
      const dir = `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
      await mkdir(dir, { recursive: true });
      await connect.screenshot({
        path: `${dir}/guide-marks-${id}-${scheme}.png`,
        animations: 'disabled',
      });
      // Every mark lies within its picture.
      const frame = await connect.locator('.guide-shot-frame').boundingBox();
      for (const box of await connect
        .locator('.guide-mark')
        .evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().toJSON())))
        expect(
          box.left >= frame!.x - 1 &&
            box.top >= frame!.y - 1 &&
            box.right <= frame!.x + frame!.width + 1 &&
            box.bottom <= frame!.y + frame!.height + 1,
          `${id} mark inside the picture`,
        ).toBe(true);
    }
    await context.close();
  }
});
