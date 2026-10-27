import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';

// Appearance acceptance (handoff_ui.md 6 and 7, round 2): follow the system until the user picks
// light or dark, keep that choice, no flash of the other scheme, every page readable in both, and
// one icon set on every route. It runs on the payment fixture, which has administrators, so the
// console is covered too; themea and themeroot are this spec's own accounts.
type Fixture = {
  api: string;
  gateway: string;
  password: string;
  control: string;
  emails: { themea: string; themeroot: string };
};
type FixtureOrder = { id: string; trade_no: string };
type Scheme = 'light' | 'dark';

// globals.css --al-bg, as computed.
const pageBackground: Record<Scheme, string> = {
  light: 'rgb(247, 248, 250)',
  dark: 'rgb(18, 26, 24)',
};
const storageKey = 'asterlink-color-scheme';

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

async function control(path: string, method = 'GET') {
  const response = await fetch(fixture.api + '/fixture/' + path, {
    method,
    headers: { 'X-Acceptance-Control': fixture.control },
  });
  if (!response.ok && response.status !== 204)
    throw new Error(`fixture ${path}: ${response.status}`);
  return response.status === 204 ? null : response.json();
}

async function login(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

const html = (page: Page) => page.locator('html');
async function expectScheme(page: Page, scheme: Scheme) {
  await expect(html(page)).toHaveAttribute('data-mantine-color-scheme', scheme);
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.body).backgroundColor))
    .toBe(pageBackground[scheme]);
}

async function choose(page: Page, label: '浅色' | '深色' | '跟随系统') {
  await page.getByRole('button', { name: '切换外观' }).click();
  await page.getByRole('menuitem', { name: label }).click();
}

// What the page looked like before any app script could change it: the scheme when <body> is
// first inserted (only the head script has run by then) and the scheme and page colour at
// DOMContentLoaded (handoff_ui.md 11, round 2).
const recordFirstPaint = () => {
  const w = window as unknown as { __first?: Record<string, string | null> };
  w.__first = {};
  const scheme = () => document.documentElement.getAttribute('data-mantine-color-scheme');
  new MutationObserver((_, observer) => {
    if (document.body) {
      w.__first!.atBody = scheme();
      observer.disconnect();
    }
  }).observe(document, { childList: true, subtree: true });
  document.addEventListener('DOMContentLoaded', () => {
    w.__first!.atReady = scheme();
    w.__first!.background = getComputedStyle(document.body).backgroundColor;
  });
};
async function firstPaint(page: Page) {
  await page.waitForFunction(() => {
    const w = window as unknown as { __first?: Record<string, string> };
    return Boolean(w.__first?.background);
  });
  return page.evaluate(() => (window as unknown as { __first: Record<string, string> }).__first);
}

async function context(browser: Browser, colorScheme: Scheme, storageState?: string) {
  const created = await browser.newContext({
    colorScheme,
    ...(storageState ? { storageState: JSON.parse(storageState) } : {}),
  });
  await created.addInitScript(recordFirstPaint);
  return created;
}
const state = async (ctx: BrowserContext) => JSON.stringify(await ctx.storageState());

test.describe.configure({ mode: 'serial' });

test('a first visit follows the system, live, with no flash of the other scheme', async ({
  browser,
}) => {
  for (const scheme of ['dark', 'light'] as const) {
    const ctx = await context(browser, scheme);
    const page = await ctx.newPage();
    for (const path of ['/login', '/plans', '/']) {
      await page.goto(path);
      expect(await firstPaint(page), `${path} ${scheme}`).toEqual({
        atBody: scheme,
        atReady: scheme,
        background: pageBackground[scheme],
      });
      await expectScheme(page, scheme);
    }
    // Nothing is stored until the user chooses.
    expect(await page.evaluate((key) => localStorage.getItem(key), storageKey)).toBeNull();
    // The system changes while the page is open: the page follows at once.
    const other = scheme === 'dark' ? 'light' : 'dark';
    await page.emulateMedia({ colorScheme: other });
    await expectScheme(page, other);
    await ctx.close();
  }
});

test('a manual choice is kept across reloads, tabs and restarts; follow-system follows again', async ({
  browser,
}) => {
  const ctx = await context(browser, 'light');
  const page = await ctx.newPage();
  await page.goto('/login');
  await expectScheme(page, 'light');
  const second = await ctx.newPage();
  await second.goto('/plans');

  await choose(page, '深色');
  await expectScheme(page, 'dark');
  expect(await page.evaluate((key) => localStorage.getItem(key), storageKey)).toBe('dark');
  // The other open tab follows (storage event).
  await expectScheme(second, 'dark');
  // The menu marks the current choice.
  await page.getByRole('button', { name: '切换外观' }).click();
  await expect(page.getByRole('menuitem', { name: '深色' })).toHaveAttribute(
    'aria-current',
    'true',
  );
  await expect(page.getByRole('menuitem', { name: '浅色' })).not.toHaveAttribute('aria-current');
  await page.keyboard.press('Escape');

  // Reload, the system turning light, and a new tab: still dark, from the first paint on.
  await page.reload();
  expect(await firstPaint(page)).toEqual({
    atBody: 'dark',
    atReady: 'dark',
    background: pageBackground.dark,
  });
  await page.emulateMedia({ colorScheme: 'light' });
  await expectScheme(page, 'dark');
  const third = await ctx.newPage();
  await third.goto('/register');
  expect((await firstPaint(third)).atBody).toBe('dark');

  // A restarted browser with the same storage (and a light system): dark.
  const kept = await state(ctx);
  await ctx.close();
  const restarted = await context(browser, 'light', kept);
  const again = await restarted.newPage();
  await again.goto('/');
  expect(await firstPaint(again)).toEqual({
    atBody: 'dark',
    atReady: 'dark',
    background: pageBackground.dark,
  });

  // Light against a dark system is kept the same way.
  await again.emulateMedia({ colorScheme: 'dark' });
  await choose(again, '浅色');
  await expectScheme(again, 'light');
  await again.reload();
  expect((await firstPaint(again)).atBody).toBe('light');
  await expectScheme(again, 'light');

  // Choosing "follow the system" is stored too, and the page follows again.
  await choose(again, '跟随系统');
  expect(await again.evaluate((key) => localStorage.getItem(key), storageKey)).toBe('auto');
  await expectScheme(again, 'dark');
  await again.emulateMedia({ colorScheme: 'light' });
  await expectScheme(again, 'light');
  await restarted.close();
});

test('with storage unavailable the page follows the system and reports no error', async ({
  browser,
}) => {
  const ctx = await context(browser, 'dark');
  await ctx.addInitScript(() => {
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get() {
        throw new DOMException('The operation is insecure.', 'SecurityError');
      },
    });
  });
  const page = await ctx.newPage();
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (entry) => {
    // The browser logs the signed-out /auth/me answer (401); that is not the page's error.
    if (entry.type() === 'error' && !entry.text().includes('status of 401'))
      errors.push(entry.text());
  });
  await page.goto('/login');
  expect(await firstPaint(page)).toEqual({
    atBody: 'dark',
    atReady: 'dark',
    background: pageBackground.dark,
  });
  await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible();
  await page.emulateMedia({ colorScheme: 'light' });
  await expectScheme(page, 'light');
  // Choosing still works for this page; nothing can be kept.
  await choose(page, '深色');
  await expectScheme(page, 'dark');
  await page.goto('/plans');
  await expect(page.locator('h1').first()).toBeVisible();
  expect(errors).toEqual([]);
  await ctx.close();
});

// WCAG contrast of an element's text against its own background, or the page's when it has none.
async function contrast(page: Page, selector: ReturnType<Page['locator']>) {
  return selector.evaluate((element) => {
    const rgb = (value: string) => {
      const m = /rgba?\((\d+), (\d+), (\d+)(?:, ([\d.]+))?\)/.exec(value)!;
      return { c: [m[1], m[2], m[3]].map(Number), a: m[4] === undefined ? 1 : Number(m[4]) };
    };
    let node: Element | null = element;
    let background = rgb('rgba(0, 0, 0, 0)');
    while (node && background.a < 1) {
      background = rgb(getComputedStyle(node).backgroundColor);
      node = node.parentElement;
    }
    if (background.a < 1) background = rgb(getComputedStyle(document.body).backgroundColor);
    const luminance = (c: number[]) => {
      const [r, g, b] = c.map((v) => {
        const s = v / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * r + 0.7152 * g + 0.0722 * b;
    };
    const [x, y] = [luminance(rgb(getComputedStyle(element).color).c), luminance(background.c)];
    return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
  });
}

test('text and the primary button meet AA contrast in both schemes', async ({ browser }) => {
  for (const scheme of ['light', 'dark'] as const) {
    const ctx = await context(browser, scheme);
    const page = await ctx.newPage();
    await page.goto('/login');
    await expectScheme(page, scheme);
    const checks = {
      heading: page.getByRole('heading', { name: '欢迎回来' }),
      dimmed: page.getByText('登录你的账户，继续享受安全连接。'),
      button: page.getByRole('button', { name: '登录', exact: true }),
      link: page.getByRole('link', { name: '创建账户' }),
    };
    for (const [name, locator] of Object.entries(checks)) {
      const ratio = await contrast(page, locator);
      expect(ratio, `${scheme} ${name} ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(4.5);
    }
    await ctx.close();
  }
});

type IconLink = { rel: string; href: string; type: string | null; sizes: string | null };
const iconLinks = (page: Page) =>
  page.evaluate(() =>
    [
      ...document.querySelectorAll(
        'link[rel~="icon"], link[rel="apple-touch-icon"], link[rel="manifest"]',
      ),
    ].map((link) => ({
      rel: link.getAttribute('rel')!,
      href: link.getAttribute('href')!,
      type: link.getAttribute('type'),
      sizes: link.getAttribute('sizes'),
    })),
  );

test('every route carries the same icons, each served with its type', async ({ page }) => {
  const seen: Record<string, IconLink[]> = {};
  for (const path of ['/', '/login', '/plans', '/no-such-page']) {
    await page.goto(path);
    seen[path] = await iconLinks(page);
  }
  await login(page, fixture.emails.themeroot);
  for (const path of ['/dashboard', '/admin/console/users']) {
    await page.goto(path);
    await expect(page.locator('h1').first()).toBeVisible();
    seen[path] = await iconLinks(page);
  }
  const reference = seen['/'];
  expect(reference.map(({ rel }) => rel).sort()).toEqual(
    ['apple-touch-icon', 'icon', 'icon', 'manifest'].sort(),
  );
  for (const [path, links] of Object.entries(seen)) expect(links, path).toEqual(reference);

  const types: Record<string, string> = {
    '/favicon.ico': 'image/x-icon',
    '/icons/icon-192.png': 'image/png',
    '/icons/icon-512.png': 'image/png',
  };
  for (const { href } of reference) {
    const path = new URL(href, 'http://x').pathname;
    types[href] = path.endsWith('.svg')
      ? 'image/svg+xml'
      : path.endsWith('.png')
        ? 'image/png'
        : path.endsWith('.ico')
          ? 'image/x-icon'
          : 'application/manifest+json';
  }
  for (const [href, type] of Object.entries(types)) {
    const response = await page.request.get(href);
    expect(response.status(), href).toBe(200);
    expect(response.headers()['content-type'], href).toContain(type);
  }
  const manifest = await (
    await page.request.get(reference.find(({ rel }) => rel === 'manifest')!.href)
  ).json();
  expect(manifest.icons.map((icon: { src: string }) => icon.src)).toEqual([
    '/icons/icon-192.png',
    '/icons/icon-512.png',
  ]);
  // The home-screen icon is opaque to the corner (iOS fills transparency with black).
  const apple = reference.find(({ rel }) => rel === 'apple-touch-icon')!.href;
  const corner = await page.evaluate(async (src) => {
    const image = new Image();
    image.src = src;
    await image.decode();
    const canvas = document.createElement('canvas');
    canvas.width = image.width;
    canvas.height = image.height;
    const context = canvas.getContext('2d')!;
    context.drawImage(image, 0, 0);
    return [image.width, context.getImageData(0, 0, 1, 1).data[3]];
  }, apple);
  expect(corner).toEqual([180, 255]);
  // The browser bar colour follows the system appearance.
  const themeColors = await page
    .locator('meta[name="theme-color"]')
    .evaluateAll((metas) => metas.map((meta) => meta.getAttribute('media')));
  expect(themeColors).toEqual(['(prefers-color-scheme: light)', '(prefers-color-scheme: dark)']);
});

// Settled: the main heading shown and no skeleton or loader left.
async function settle(page: Page) {
  await expect(page.locator('h1').first()).toBeVisible();
  await expect(page.locator('.mantine-Skeleton-root')).toHaveCount(0);
  await expect(page.locator('.mantine-Loader-root')).toHaveCount(0);
}

// Leftovers of the light design in dark: visible boxes painted (near) white. Buttons in the white
// variant and switch thumbs are white by design.
async function lightBoxes(page: Page) {
  return page.evaluate(() => {
    const found: string[] = [];
    for (const element of document.querySelectorAll<HTMLElement>('body *')) {
      const box = element.getBoundingClientRect();
      if (box.width * box.height < 64) continue;
      if (element.closest('[data-variant="white"], .mantine-Switch-thumb, .impersonation-banner'))
        continue;
      const style = getComputedStyle(element);
      if (style.visibility === 'hidden' || style.display === 'none' || style.opacity === '0')
        continue;
      const match = /rgba?\((\d+), (\d+), (\d+)(?:, ([\d.]+))?\)/.exec(style.backgroundColor);
      if (!match) continue;
      const [r, g, b] = match.slice(1, 4).map(Number);
      const alpha = match[4] === undefined ? 1 : Number(match[4]);
      if (alpha > 0.5 && Math.min(r, g, b) > 200)
        found.push(
          `${element.tagName.toLowerCase()}.${[...element.classList].join('.')} ${style.backgroundColor}`,
        );
    }
    return found;
  });
}

type Report = Record<string, { scheme: Scheme; lightBoxes: string[] }>;
const report: Report = {};
async function review(page: Page, path: string, scheme: Scheme, name: string) {
  await page.goto(path);
  await settle(page);
  await expectScheme(page, scheme);
  if (scheme === 'dark') {
    const boxes = await lightBoxes(page);
    report[`${name}-${scheme}`] = { scheme, lightBoxes: boxes };
    expect(boxes, `${path} in dark`).toEqual([]);
  }
  await shot(page, `theme-${scheme}-${name}`);
}

test('the customer pages read well in both schemes', async ({ browser }) => {
  // A paying customer, so the overview shows the subscription banner and the usage panels.
  const ctx = await browser.newContext({ colorScheme: 'dark' });
  const page = await ctx.newPage();
  await login(page, fixture.emails.themea);
  await page.goto('/plans');
  await page.getByRole('button', { name: '购买 轻量计划', exact: true }).click();
  await page.getByRole('radio', { name: '支付宝' }).click();
  await shot(page, 'theme-dark-purchase-modal');
  await page.getByRole('button', { name: '去支付', exact: true }).click();
  await page.waitForURL((url) => url.href.startsWith(fixture.gateway + '/submit?'));
  const orders = (await control(
    'orders?email=' + encodeURIComponent(fixture.emails.themea),
  )) as FixtureOrder[];
  const order = orders.at(-1)!;
  await control(`pay?trade_no=${order.trade_no}`, 'POST');
  await page.waitForURL(/\/pay\/result\?order=/, { timeout: 30000 });
  await expect(page.getByRole('heading', { name: '已到账', exact: true })).toBeVisible();

  const pages: [string, string][] = [
    ['/dashboard', 'dashboard'],
    ['/nodes', 'nodes'],
    ['/plans', 'plans'],
    ['/orders', 'orders'],
    ['/referral', 'referral'],
    ['/download', 'download'],
    ['/account', 'account'],
    [`/pay/result?order=${order.id}`, 'pay-result'],
    ['/no-such-page', 'not-found'],
  ];
  for (const scheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const [path, name] of pages) await review(page, path, scheme, name);
  }
  // Phone width, dark: the header with the appearance switch, the drawer and the plan cards.
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.setViewportSize({ width: 390, height: 844 });
  for (const [path, name] of [
    ['/dashboard', 'dashboard'],
    ['/plans', 'plans'],
  ])
    await review(page, path, 'dark', `mobile-${name}`);
  const toggle = page.getByRole('button', { name: '切换外观' });
  const size = await toggle.boundingBox();
  expect(size!.width).toBeGreaterThanOrEqual(40);
  expect(size!.height).toBeGreaterThanOrEqual(40);
  await page.getByRole('button', { name: '展开导航' }).click();
  await shot(page, 'theme-dark-mobile-navigation');
  await ctx.close();

  // Signed out: home, sign-in pages and the public plan list.
  const visitor = await browser.newContext({ colorScheme: 'dark' });
  const anonymous = await visitor.newPage();
  for (const scheme of ['dark', 'light'] as const) {
    await anonymous.emulateMedia({ colorScheme: scheme });
    for (const [path, name] of [
      ['/', 'home'],
      ['/login', 'login'],
      ['/register', 'register'],
      ['/forgot-password', 'forgot-password'],
      ['/reset-password', 'reset-password'],
      ['/plans', 'plans-public'],
    ])
      await review(anonymous, path, scheme, name);
  }
  await anonymous.emulateMedia({ colorScheme: 'dark' });
  await anonymous.setViewportSize({ width: 390, height: 844 });
  await review(anonymous, '/login', 'dark', 'mobile-login');
  await visitor.close();
});

test('the administration reads well in both schemes', async ({ browser }) => {
  const ctx = await browser.newContext({ colorScheme: 'dark' });
  const page = await ctx.newPage();
  await login(page, fixture.emails.themeroot);
  const pages: [string, string][] = [
    ['/admin/console/users', 'admin-users'],
    ['/admin/console/orders', 'admin-orders'],
    ['/admin/console/plans', 'admin-plans'],
    ['/admin/console/logins', 'admin-logins'],
    ['/admin/console/coupons', 'admin-coupons'],
    ['/admin/console/announcements', 'admin-announcements'],
    ['/admin/nodes', 'admin-nodes'],
  ];
  for (const scheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const [path, name] of pages) await review(page, path, scheme, name);
  }
  // A drawer over the console in dark.
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.goto('/admin/console/users');
  await page.getByRole('button', { name: `查看 ${fixture.emails.themea}` }).click();
  await expect(page.getByRole('dialog', { name: /^用户详情/ })).toBeVisible();
  await shot(page, 'theme-dark-admin-user-drawer');
  await ctx.close();
  await mkdir(screens(), { recursive: true });
  await writeFile(`${screens()}/../theme-report.json`, JSON.stringify(report, null, 2));
});
