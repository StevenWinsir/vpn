import { expect, test, type Browser, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import { bannedIn } from './copy-words';
import { overflowing } from './layout';

// Home page acceptance (handoff_ui.md 8 and 11, round 3), on the payment fixture, which looks like
// production: registration open, the gateway on, Android "released" and iOS not, a filing number
// and a service address in the footer (scripts/test-payment-web.py). homea only signs in.
type Fixture = { api: string; password: string; emails: { homea: string } };

let fixture: Fixture;
test.beforeAll(async () => {
  const filename = process.env.PAYMENT_FIXTURE;
  if (!filename?.startsWith('/tmp/vpn-payment-'))
    throw new Error('Private payment fixture required');
  fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
});

const screens = () => `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
async function shot(page: Page, name: string) {
  await mkdir(screens(), { recursive: true });
  await page.screenshot({
    path: `${screens()}/${name}.png`,
    animations: 'disabled',
    fullPage: true,
  });
}

async function login(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

const header = (page: Page) => page.locator('header.home-header');
const hero = (page: Page) => page.getByTestId('hero-actions');

// Loaded: the account actions decided and the plan preview resolved one way or the other.
async function settle(page: Page) {
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await expect(page.getByTestId('header-pending')).toHaveCount(0);
  await expect(page.getByTestId('action-pending')).toHaveCount(0);
  await expect(page.getByTestId('plan-skeleton')).toHaveCount(0);
}

// Every distinct label the header and hero actions ever showed, from the first paint on.
const recordActions = () => {
  const w = window as unknown as { __seen: string[] };
  w.__seen = [];
  new MutationObserver(() => {
    for (const element of document.querySelectorAll(
      '.home-header-actions a, [data-testid="hero-actions"] a',
    )) {
      const text = (element.textContent || '').trim();
      if (!w.__seen.includes(text)) w.__seen.push(text);
    }
  }).observe(document, { subtree: true, childList: true, characterData: true });
};
const seen = (page: Page) =>
  page.evaluate(() => (window as unknown as { __seen: string[] }).__seen);

test.describe.configure({ mode: 'serial' });

test('structure, copy and footer of the home page', async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto('/');
  await settle(page);
  await expect(page).toHaveTitle(/^AsterLink · 连接，自有边界$/);
  // Landmarks: the marketing header (not the workspace shell), one main, the footer.
  await expect(page.locator('.mantine-AppShell-root')).toHaveCount(0);
  await expect(page.getByRole('main')).toHaveCount(1);
  await expect(page.getByRole('contentinfo')).toBeVisible();
  const nav = page.getByRole('navigation', { name: '首页导航', exact: true });
  for (const [label, id] of [
    ['产品能力', 'features'],
    ['套餐价格', 'pricing'],
    ['客户端', 'clients'],
    ['常见问题', 'faq'],
  ])
    await expect(nav.getByRole('link', { name: label })).toHaveAttribute('href', `#${id}`);
  await expect(header(page).getByRole('button', { name: '切换外观' })).toBeVisible();
  // Registration is open on this fixture.
  await expect(header(page).getByRole('link', { name: '登录', exact: true })).toBeVisible();
  await expect(header(page).getByRole('link', { name: '免费注册' })).toHaveAttribute(
    'href',
    '/register',
  );
  await expect(hero(page).getByRole('link', { name: '创建账户' })).toHaveAttribute(
    'href',
    '/register',
  );
  await expect(hero(page).getByRole('link', { name: '查看套餐' })).toHaveAttribute(
    'href',
    '/plans',
  );
  for (const heading of [
    '为稳定连接而设计的每一处细节',
    '三步开始使用',
    '简单透明的套餐价格',
    '一个账户，多端通用',
    '你可能想了解',
    '现在开始，享受稳定安心的连接',
  ])
    await expect(page.getByRole('heading', { level: 2, name: heading })).toBeVisible();
  await expect(page.locator('.home-feature')).toHaveCount(6);
  await expect(page.locator('.home-step')).toHaveCount(3);

  // Copy: the visible text and every FAQ answer, against the development and D12 vocabulary.
  const faq = page.getByTestId('home-faq').getByRole('button');
  // The service address is configured, so the "contact us" answer is there too.
  await expect(faq).toHaveCount(7);
  const answers: string[] = [];
  for (let i = 0; i < 7; i++) {
    const button = faq.nth(i);
    if ((await button.getAttribute('aria-expanded')) !== 'true') await button.click();
    const region = page.locator(`#${await button.getAttribute('aria-controls')}`);
    await expect(region).toBeVisible();
    answers.push(await region.innerText());
  }
  expect(answers.join('\n')).toContain('计费流量 = 实际上传与下载流量 × 线路倍率');
  expect(answers.join('\n')).toContain('微信支付和支付宝');
  expect(answers.join('\n')).toContain('support@example.test');
  expect(answers.join('\n')).not.toContain('退款');
  const text = (await page.evaluate(() => document.body.innerText)) + answers.join('\n');
  expect(bannedIn(text)).toEqual([]);

  // Footer: existing pages only, the service address, the filing number linked to MIIT.
  const footer = page.getByRole('contentinfo');
  for (const [label, href] of [
    ['套餐价格', '/plans'],
    ['下载客户端', '/download'],
    ['登录', '/login'],
    ['注册', '/register'],
    ['找回密码', '/forgot-password'],
  ])
    await expect(footer.getByRole('link', { name: label, exact: true })).toHaveAttribute(
      'href',
      href,
    );
  await expect(footer.getByTestId('support-email')).toHaveAttribute(
    'href',
    'mailto:support@example.test',
  );
  const icp = footer.getByTestId('icp-record');
  await expect(icp).toHaveText('京ICP备00000000号-1');
  await expect(icp).toHaveAttribute('href', 'https://beian.miit.gov.cn/');
  await expect(icp).toHaveAttribute('rel', 'noopener noreferrer');
  await expect(footer).toContainText(`© ${new Date().getFullYear()} AsterLink`);
  expect(errors).toEqual([]);
});

test('no request leaves the site, including the plan preview and the whole scroll', async ({
  page,
  baseURL,
}) => {
  const origin = new URL(baseURL!).origin;
  const foreign: string[] = [];
  page.on('request', (request) => {
    const url = request.url();
    if (!/^(data|blob):/.test(url) && new URL(url).origin !== origin) foreign.push(url);
  });
  await page.goto('/');
  await settle(page);
  for (let y = 0; y < 20; y++) {
    await page.mouse.wheel(0, 900);
    await page.waitForTimeout(100);
  }
  await page.waitForLoadState('networkidle');
  expect(foreign).toEqual([]);
});

test('the plan preview shows exactly what the plan page sells', async ({ page }) => {
  await page.goto('/');
  await settle(page);
  const cards = page.getByTestId('home-plan');
  await expect(cards.first()).toBeVisible();
  const shown = await cards.evaluateAll((elements) =>
    elements.map((element) => ({
      id: (element as HTMLElement).dataset.plan!,
      featured: element.classList.contains('featured'),
      price: element.querySelector('[data-field="price"]')!.textContent!,
      traffic: element.querySelector('[data-field="traffic"]')!.textContent!,
      devices: element.querySelector('[data-field="devices"]')!.textContent!,
    })),
  );
  expect(shown.length).toBeGreaterThan(0);
  expect(shown.length).toBeLessThanOrEqual(3);
  const promo = await page.getByTestId('home-yearly-promo').count();
  await page.goto('/plans');
  await expect(page.getByTestId('plan-card').first()).toBeVisible();
  const ids = await page
    .getByTestId('plan-card')
    .evaluateAll((elements) => elements.map((e) => (e as HTMLElement).dataset.plan!));
  // Same order as the shop, featured plans never left out.
  expect(shown.map((p) => p.id)).toEqual(ids.filter((id) => shown.some((p) => p.id === id)));
  for (const plan of shown) {
    const card = page.locator(`[data-testid="plan-card"][data-plan="${plan.id}"]`);
    await expect(card).toContainText(plan.price);
    await expect(card).toContainText(plan.traffic);
    await expect(card).toContainText(plan.devices);
    expect(await card.evaluate((e) => e.classList.contains('featured'))).toBe(plan.featured);
  }
  // The yearly hint appears exactly when the shop has a yearly discount to switch to.
  expect(promo).toBe(await page.getByTestId('cycle-badge').count());
});

test('the plan preview disappears quietly when the plans fail or none are on sale', async ({
  page,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  for (const answer of [
    {
      status: 503,
      body: { error: { code: 'database_unavailable', message: '套餐暂时不可用' } },
    },
    {
      status: 200,
      body: { plans: [], yearly_discount_percent: 100, max_yearly_saving_percent: 0 },
    },
  ]) {
    await page.route('**/api/v1/plans', (route) =>
      route.fulfill({
        status: answer.status,
        contentType: 'application/json',
        body: JSON.stringify(answer.body),
      }),
    );
    await page.goto('/');
    await settle(page);
    const pricing = page.locator('#pricing');
    await expect(pricing.getByTestId('home-plans')).toHaveCount(0);
    await expect(pricing.getByTestId('home-yearly-promo')).toHaveCount(0);
    await expect(pricing.getByRole('link', { name: '查看全部套餐' })).toBeVisible();
    // Next.js keeps an empty route announcer with the alert role; no alert with text may appear.
    await expect(page.getByRole('alert').filter({ hasText: /\S/ })).toHaveCount(0);
    await expect(pricing).not.toContainText('不可用');
    await expect(pricing).not.toContainText('失败');
    await page.unroute('**/api/v1/plans');
  }
  expect(errors).toEqual([]);
});

test('the main action follows registration and the account, without flashing', async ({
  browser,
}) => {
  // Registration closed (/meta says so): sign-in only.
  let ctx = await browser.newContext();
  let page = await ctx.newPage();
  await page.route('**/api/v1/meta', async (route) => {
    const response = await route.fetch();
    const meta = await response.json();
    await route.fulfill({ response, json: { ...meta, allow_registration: false } });
  });
  await page.goto('/');
  await settle(page);
  await expect(hero(page).getByRole('link', { name: '登录账户' })).toHaveAttribute(
    'href',
    '/login',
  );
  await expect(header(page).getByRole('link', { name: '免费注册' })).toHaveCount(0);
  await expect(page.locator('.home-cta').getByRole('link', { name: '登录账户' })).toBeVisible();
  await ctx.close();

  // /meta failing counts as closed.
  ctx = await browser.newContext();
  page = await ctx.newPage();
  await page.route('**/api/v1/meta', (route) => route.fulfill({ status: 503, json: {} }));
  await page.goto('/');
  await settle(page);
  await expect(hero(page).getByRole('link', { name: '登录账户' })).toBeVisible();
  await ctx.close();

  // Open registration and a slow /meta: the visitor never sees "登录账户" first.
  ctx = await browser.newContext();
  await ctx.addInitScript(recordActions);
  page = await ctx.newPage();
  await page.route('**/api/v1/meta', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    await route.continue();
  });
  await page.goto('/');
  await settle(page);
  await expect(hero(page).getByRole('link', { name: '创建账户' })).toBeVisible();
  expect((await seen(page)).sort()).toEqual(['免费注册', '创建账户', '查看套餐', '登录'].sort());
  await ctx.close();

  // Signed in, with a slow /auth/me: the header goes straight to the user centre.
  ctx = await browser.newContext();
  page = await ctx.newPage();
  await login(page, fixture.emails.homea);
  await ctx.addInitScript(recordActions);
  await page.route('**/api/v1/auth/me', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    await route.continue();
  });
  await page.goto('/');
  await settle(page);
  await expect(header(page).getByRole('link', { name: '进入用户中心' })).toHaveAttribute(
    'href',
    '/dashboard',
  );
  await expect(hero(page).getByRole('link', { name: '进入用户中心' })).toBeVisible();
  expect((await seen(page)).sort()).toEqual(['进入用户中心', '查看套餐'].sort());
  await shot(page, 'home-signed-in');
  await ctx.close();
});

test('the client cards match the download page', async ({ page }) => {
  await page.goto('/');
  await settle(page);
  const card = (name: string) =>
    page.locator(`[data-testid="home-platform"][data-platform="${name}"]`);
  // Android is released on this fixture, iOS is not; the desktop clients are not offered yet.
  await expect(card('Android')).toHaveAttribute('data-ready', 'true');
  await expect(
    card('Android').getByRole('link', { name: '立即下载 Android 客户端' }),
  ).toHaveAttribute('href', '/download');
  for (const name of ['iOS', 'macOS', 'Windows']) {
    await expect(card(name)).toHaveAttribute('data-ready', 'false');
    await expect(card(name)).toContainText('即将上线');
    await expect(card(name).getByRole('link')).toHaveCount(0);
  }
  await page.goto('/download');
  await expect(page.getByRole('link', { name: /下载 APK/ })).toHaveAttribute(
    'href',
    /^https:\/\/downloads\.example\.test\//,
  );
  await expect(page.getByText('iPhone 版本暂未发布，请稍后再来。')).toBeVisible();
});

test('anchors land below the sticky header; the keyboard reaches everything', async ({ page }) => {
  await page.goto('/');
  await settle(page);
  const headerBox = await header(page).boundingBox();
  for (const [label, id] of [
    ['常见问题', 'faq'],
    ['套餐价格', 'pricing'],
    ['产品能力', 'features'],
  ]) {
    await page
      .getByRole('navigation', { name: '首页导航', exact: true })
      .getByRole('link', { name: label })
      .click();
    await expect(page).toHaveURL(new RegExp(`#${id}$`));
    // Smooth scrolling settles with the section's head just under the header.
    await expect
      .poll(() => page.locator(`#${id}`).evaluate((e) => Math.round(e.getBoundingClientRect().top)))
      .toBeGreaterThanOrEqual(Math.floor(headerBox!.height) - 2);
    await expect
      .poll(() => page.locator(`#${id}`).evaluate((e) => Math.round(e.getBoundingClientRect().top)))
      .toBeLessThanOrEqual(Math.ceil(headerBox!.height) + 24);
    const heading = page.locator(`#${id} h2`);
    expect((await heading.boundingBox())!.y).toBeGreaterThan(headerBox!.height);
  }

  // Tab order from the top: brand, the four anchors, the appearance switch, the account actions,
  // then the hero; focus is visible.
  await page.goto('/');
  await settle(page);
  const order: string[] = [];
  for (let i = 0; i < 10; i++) {
    await page.keyboard.press('Tab');
    order.push(
      await page.evaluate(() => {
        const element = document.activeElement as HTMLElement;
        return element.getAttribute('aria-label') || element.innerText.trim().split('\n')[0];
      }),
    );
  }
  expect(order).toEqual([
    'AsterLink',
    '产品能力',
    '套餐价格',
    '客户端',
    '常见问题',
    '切换外观',
    '登录',
    '免费注册',
    '创建账户',
    '查看套餐',
  ]);
  await page.keyboard.press('Shift+Tab');
  expect(
    await page.evaluate(() => getComputedStyle(document.activeElement!).outlineStyle),
  ).not.toBe('none');

  // FAQ: Enter and Space open and close a question.
  const question = page.getByRole('button', { name: '可以同时在线几台设备？' });
  await question.focus();
  await page.keyboard.press('Enter');
  await expect(question).toHaveAttribute('aria-expanded', 'true');
  await expect(page.getByRole('region', { name: '可以同时在线几台设备？' })).toContainText(
    '以所购套餐为准',
  );
  await page.keyboard.press('Space');
  await expect(question).toHaveAttribute('aria-expanded', 'false');
  await expect(page.getByRole('region', { name: '可以同时在线几台设备？' })).toBeHidden();
});

test('motion is gentle and off with reduced motion', async ({ browser }) => {
  const motion = async (reducedMotion: 'reduce' | 'no-preference') => {
    const ctx = await browser.newContext({ reducedMotion });
    const page = await ctx.newPage();
    await page.goto('/');
    await settle(page);
    const before = await page.locator('[data-reveal="hidden"]').count();
    for (let y = 0; y < 20; y++) {
      await page.mouse.wheel(0, 900);
      await page.waitForTimeout(80);
    }
    await page.waitForTimeout(1000);
    const result = {
      hiddenAtLoad: before,
      hiddenAfterScroll: await page.locator('[data-reveal="hidden"]').count(),
      animations: await page.evaluate(() => document.getAnimations().length),
      scrollBehavior: await page.evaluate(
        () => getComputedStyle(document.documentElement).scrollBehavior,
      ),
    };
    await ctx.close();
    return result;
  };
  const calm = await motion('reduce');
  expect(calm).toEqual({
    hiddenAtLoad: 0,
    hiddenAfterScroll: 0,
    animations: 0,
    scrollBehavior: 'auto',
  });
  const lively = await motion('no-preference');
  // Sections below the fold wait for the viewport and are all shown once scrolled past.
  expect(lively.hiddenAtLoad).toBeGreaterThan(0);
  expect(lively.hiddenAfterScroll).toBe(0);
  expect(lively.animations).toBeGreaterThan(0);
  expect(lively.scrollBehavior).toBe('smooth');
});

async function review(browser: Browser, scheme: 'light' | 'dark', width: number) {
  const ctx = await browser.newContext({
    colorScheme: scheme,
    viewport: { width, height: width < 768 ? 844 : 900 },
    // Every section visible at once for the full-page picture.
    reducedMotion: 'reduce',
  });
  const page = await ctx.newPage();
  await page.goto('/');
  await settle(page);
  await expect(page.getByTestId('home-plans')).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('data-mantine-color-scheme', scheme);
  expect(await overflowing(page), `${scheme} ${width}`).toEqual([]);
  await shot(page, `home-${scheme}-${width}`);
  if (width < 900) {
    // The folded menu: anchors and account actions, closing on a choice.
    await page.getByRole('button', { name: '展开菜单' }).click();
    const menu = page.locator('#home-menu');
    await expect(menu.getByRole('link', { name: '常见问题' })).toBeVisible();
    await expect(menu.getByRole('link', { name: '免费注册' })).toBeVisible();
    expect(await overflowing(page), `${scheme} ${width} menu`).toEqual([]);
    if (width === 390) await page.screenshot({ path: `${screens()}/home-${scheme}-390-menu.png` });
    await menu.getByRole('link', { name: '常见问题' }).click();
    await expect(menu).toHaveCount(0);
    await expect(page).toHaveURL(/#faq$/);
  } else {
    await expect(page.getByRole('button', { name: '展开菜单' })).toBeHidden();
  }
  await ctx.close();
}

test('light and dark at 1440, 1024, 768, 390 and 360 without sideways scrolling', async ({
  browser,
}) => {
  for (const scheme of ['light', 'dark'] as const)
    for (const width of [1440, 1024, 768, 390, 360]) await review(browser, scheme, width);
});
