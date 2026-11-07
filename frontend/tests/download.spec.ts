import { expect, test, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import { overflowing } from './layout';

// Browser acceptance for the download page, its redirects and the iPhone manifest
// (handoff_download.md 6 and 9, DL3-01 … DL3-11), against the real API on the private PostgreSQL
// of scripts/test-payment-web.py. The fixture's download sets: base (Android arm64-v8a only),
// multi (several platforms and architectures). Every address is on downloads.example.test, which
// is intercepted, never fetched. The website caches the list for one second here (30 in production).
type Fixture = {
  api: string;
  control: string;
  password: string;
  emails: Record<'dlroot', string>;
};

let fixture: Fixture;
test.beforeAll(async () => {
  const filename = process.env.PAYMENT_FIXTURE;
  if (!filename?.startsWith('/tmp/vpn-payment-'))
    throw new Error('Private payment fixture required');
  fixture = JSON.parse(await readFile(filename, 'utf8')) as Fixture;
});

async function downloads(set: 'base' | 'multi') {
  const response = await fetch(`${fixture.api}/fixture/downloads?set=${set}`, {
    method: 'POST',
    headers: { 'X-Acceptance-Control': fixture.control },
  });
  if (response.status !== 204) throw new Error(`fixture downloads ${set}: ${response.status}`);
  // Past the website's one-second cache.
  await new Promise((resolve) => setTimeout(resolve, 1200));
}

async function shot(page: Page, name: string) {
  const dir = `${process.env.PAYMENT_OUTPUTS ?? 'test-results'}/screens`;
  await mkdir(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${name}.png`, animations: 'disabled', fullPage: true });
}

async function signIn(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

const card = (page: Page, platform: string) =>
  page.locator(`[data-testid="download-platform"][data-platform="${platform}"]`);
const buttons = (page: Page, platform: string) =>
  card(page, platform).getByTestId('download-button');

test.describe.configure({ mode: 'serial' });
test.afterAll(async () => {
  await downloads('base');
});

test('five platforms in order, several architectures, no address anywhere in the page', async ({
  page,
}) => {
  await downloads('multi');
  const response = await page.goto('/download');
  expect(response?.status()).toBe(200);

  // DL3-01: the cards and the how-to cards in their order, every anchor present.
  expect(
    await page
      .getByTestId('download-platform')
      .evaluateAll((all) => all.map((a) => [a.id, a.getAttribute('data-platform')])),
  ).toEqual([
    ['windows', 'windows'],
    ['macos', 'macos'],
    ['android', 'android'],
    ['ios', 'ios'],
    ['linux', 'linux'],
  ]);
  expect(
    await page
      .getByTestId('download-howto')
      .evaluateAll((all) => all.map((a) => a.querySelector('h2')?.textContent)),
  ).toEqual([
    'Windows 安装、升级与卸载',
    'macOS 安装、升级与卸载',
    'Android 安装、升级与卸载',
    'iPhone 安装、升级与卸载',
  ]);
  await expect(card(page, 'linux')).toContainText('Linux 版本即将推出，敬请期待。');
  // DL3-07: the licence card is gone.
  await expect(page.getByText('开源许可与源代码')).toHaveCount(0);

  // DL3-04: Windows has two architectures of different versions, Android two of the same.
  await expect(buttons(page, 'windows')).toHaveCount(2);
  await expect(buttons(page, 'windows').nth(0)).toHaveText('下载安装程序（x64 · 70.0 MB）');
  await expect(buttons(page, 'windows').nth(1)).toHaveText('下载安装程序（ARM64 · 67.0 MB）');
  await expect(buttons(page, 'windows').nth(0)).toHaveAttribute('data-variant', 'filled');
  await expect(buttons(page, 'windows').nth(1)).toHaveAttribute('data-variant', 'light');
  await expect(card(page, 'windows').getByTestId('download-version')).toHaveCount(0);
  await expect(card(page, 'windows').getByTestId('download-button-version')).toHaveText([
    '版本 1.2.3',
    '版本 1.2.4',
  ]);
  await expect(card(page, 'android').getByTestId('download-version')).toHaveText('版本 1.2.3');
  await expect(card(page, 'android').getByTestId('download-button-version')).toHaveCount(0);
  await expect(buttons(page, 'android')).toHaveText([
    '下载 APK（64 位手机 · 45.9 MB）',
    '下载 APK（32 位旧款手机 · 43.0 MB）',
  ]);
  await expect(card(page, 'android').getByTestId('download-sha')).toHaveCount(2);
  await expect(card(page, 'android')).toContainText('签名证书 SHA-256');
  await expect(card(page, 'android').getByTestId('download-sha').nth(1)).toContainText(
    'armeabi-v7a · APK 安装包',
  );
  // The unpublished Intel build is not offered.
  await expect(buttons(page, 'macos')).toHaveText(['下载 DMG（Apple 芯片 · 80.0 MB）']);
  await expect(card(page, 'windows').getByTestId('download-sha')).toHaveCount(2);

  // DL3-05: the iPhone web install names this site's manifest; TestFlight goes through the site.
  const origin = new URL(page.url()).origin;
  await expect(buttons(page, 'ios').nth(0)).toHaveAttribute(
    'href',
    `itms-services://?action=download-manifest&url=${encodeURIComponent(`${origin}/download/get/ios/arm64/ipa/manifest.plist`)}`,
  );
  await expect(buttons(page, 'ios').nth(1)).toHaveText('通过 TestFlight 安装');
  await expect(buttons(page, 'ios').nth(1)).toHaveAttribute(
    'href',
    '/download/get/ios/arm64/testflight',
  );
  await expect(card(page, 'ios')).toContainText('仅限已登记的设备安装');
  await expect(card(page, 'ios')).toContainText('TestFlight');

  // DL3-02: every button points at the site; no address in the HTML or the page data.
  for (const href of await page
    .getByTestId('download-button')
    .evaluateAll((all) => all.map((a) => a.getAttribute('href') ?? '')))
    expect(href).toMatch(/^(\/download\/get\/|itms-services:\/\/\?action=download-manifest&url=)/);
  const html = await (await page.request.get('/download')).text();
  for (const text of [html, await page.content()]) {
    expect(text).not.toContain('downloads.example.test');
    expect(text).not.toContain('testflight.apple.com');
  }
  await shot(page, 'download-multi');
});

test('the buttons redirect to the file; withdrawn or unknown ones lead back to the card', async ({
  page,
}) => {
  await downloads('multi');
  const redirect = async (path: string) => {
    const response = await page.request.get(path, { maxRedirects: 0 });
    expect(response.status(), path).toBe(302);
    expect(response.headers()['cache-control']).toBe('no-store');
    expect(response.headers()['referrer-policy']).toBe('no-referrer');
    expect(response.headers()['x-robots-tag']).toBe('noindex');
    return response.headers()['location'];
  };
  // DL3-03
  expect(await redirect('/download/get/android/arm64-v8a/apk')).toBe(
    'https://downloads.example.test/AsterLink-1.2.3-android-arm64-v8a.apk',
  );
  expect(await redirect('/download/get/ios/arm64/testflight')).toBe(
    'https://testflight.apple.com/join/AbCdEf',
  );
  expect(await redirect('/download/get/macos/x64/dmg')).toBe('/download#macos');
  expect(await redirect('/download/get/linux/x64/appimage')).toBe('/download#linux');
  expect(await redirect('/download/get/windows/x64/dmg')).toBe('/download#windows');
  expect(await redirect('/download/get/beos/x64/exe')).toBe('/download');

  // A click follows the redirect to the file (intercepted: nothing is downloaded).
  await page.route('https://downloads.example.test/**', (route) =>
    route.fulfill({ status: 204, body: '' }),
  );
  await page.goto('/download');
  const requested = page.waitForRequest((r) =>
    r.url().startsWith('https://downloads.example.test/'),
  );
  await buttons(page, 'windows').nth(1).click();
  expect((await requested).url()).toBe(
    'https://downloads.example.test/AsterLink-Setup-arm64-1.2.4.exe',
  );

  // DL3-05: the manifest is a property list a standard parser reads, with the values escaped.
  const manifest = await page.request.get('/download/get/ios/arm64/ipa/manifest.plist');
  expect(manifest.status()).toBe(200);
  expect(manifest.headers()['content-type']).toContain('application/xml');
  expect(manifest.headers()['cache-control']).toBe('no-store');
  const xml = await manifest.text();
  const parsed = await page.evaluate((text) => {
    const doc = new DOMParser().parseFromString(text, 'application/xml');
    if (doc.querySelector('parsererror')) return null;
    const value = (key: string) =>
      [...doc.querySelectorAll('key')].find((k) => k.textContent === key)?.nextElementSibling
        ?.textContent ?? null;
    return {
      url: value('url'),
      bundle: value('bundle-identifier'),
      version: value('bundle-version'),
      kind: value('kind'),
      title: value('title'),
    };
  }, xml);
  expect(parsed).toEqual({
    url: 'https://downloads.example.test/AsterLink-1.2.3.ipa?sig=a&b=c',
    bundle: 'com.example.asterlink',
    version: '1.2.3',
    kind: 'software-package',
    title: 'AsterLink',
  });
  expect(
    (await page.request.get('/download/get/android/arm64-v8a/apk/manifest.plist')).status(),
  ).toBe(404);

  // Withdrawn: the same paths lead back to the card, the manifest is gone.
  await downloads('base');
  expect(await redirect('/download/get/ios/arm64/testflight')).toBe('/download#ios');
  expect(await redirect('/download/get/windows/arm64/exe')).toBe('/download#windows');
  expect((await page.request.get('/download/get/ios/arm64/ipa/manifest.plist')).status()).toBe(404);
});

test('a version changed in the console shows on the page, the home cards and the guide', async ({
  page,
}) => {
  await downloads('base');
  await signIn(page, fixture.emails.dlroot);
  await page.goto('/admin/console/downloads');
  await page.getByRole('button', { name: '编辑 Android · arm64-v8a · APK 安装包' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('版本号').fill('1.2.5');
  await dialog.getByRole('button', { name: '保存' }).click();
  await expect(dialog).toBeHidden();
  // DL3-09: after the (one-second) cache the page, the guide and the home page follow.
  await expect(async () => {
    await page.goto('/download');
    await expect(card(page, 'android').getByTestId('download-version')).toHaveText('版本 1.2.5', {
      timeout: 1000,
    });
  }).toPass({ timeout: 15000 });
  await page.goto('/guide/android');
  await expect(page.getByTestId('guide-download')).toContainText('当前版本 1.2.5');
  // DL3-08: the desktop guide offers the download once Windows is published.
  await page.goto('/guide/windows');
  await expect(page.getByTestId('guide-download')).toHaveAttribute('data-available', 'false');
  await downloads('multi');
  await page.goto('/guide/windows');
  await expect(page.getByTestId('guide-download')).toHaveAttribute('data-available', 'true');
  await expect(page.getByTestId('guide-download')).toContainText('当前版本 1.2.3');
  await expect(page.getByRole('link', { name: '前往下载' })).toHaveAttribute(
    'href',
    '/download#windows',
  );
  await page.goto('/');
  for (const name of ['Windows', 'macOS', 'Android', 'iOS']) {
    const home = page.locator(`[data-testid="home-platform"][data-platform="${name}"]`);
    await expect(home).toHaveAttribute('data-ready', 'true');
  }
  await expect(page.getByRole('link', { name: '立即下载 macOS 客户端' })).toHaveAttribute(
    'href',
    '/download#macos',
  );
});

test('with the backend unreachable the pages still answer and say so', async ({ page }) => {
  const offline = process.env.DOWNLOAD_OFFLINE_URL;
  if (!offline) throw new Error('DOWNLOAD_OFFLINE_URL required (scripts/test-payment-web.py)');
  // DL3-06
  const response = await page.goto(`${offline}/download`);
  expect(response?.status()).toBe(200);
  const missing = page.getByTestId('download-missing');
  await expect(missing).toHaveCount(5);
  for (const reason of await missing.evaluateAll((all) =>
    all.map((a) => a.getAttribute('data-reason')),
  ))
    expect(reason).toBe('unavailable');
  await expect(missing.first()).toHaveText('下载信息暂时无法获取，请稍后刷新。');
  await expect(page.getByTestId('download-button')).toHaveCount(0);
  await shot(page, 'download-unavailable');
  expect(
    (
      await page.request.get(`${offline}/download/get/android/arm64-v8a/apk`, { maxRedirects: 0 })
    ).headers()['location'],
  ).toBe('/download#android');
  await page.goto(`${offline}/`);
  for (const name of ['Windows', 'macOS', 'Android', 'iOS'])
    await expect(
      page.locator(`[data-testid="home-platform"][data-platform="${name}"]`),
    ).toHaveAttribute('data-ready', 'unknown');
  await expect(page.getByRole('link', { name: '前往下载页查看 Android 客户端' })).toHaveAttribute(
    'href',
    '/download',
  );
});

// WCAG contrast of an element's text against the nearest opaque background.
async function contrast(page: Page, locator: ReturnType<Page['locator']>) {
  return locator.evaluate((element) => {
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

test('phone width, light and dark: nothing sticks out, the hashes wrap, text is readable', async ({
  page,
}) => {
  await downloads('multi');
  // DL3-11
  await page.setViewportSize({ width: 375, height: 812 });
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.goto('/download');
    await expect(buttons(page, 'android')).toHaveCount(2);
    expect(await overflowing(page)).toEqual([]);
    // No button label is cut off: each wraps within its button.
    expect(
      await page.getByTestId('download-button').evaluateAll((all) =>
        all
          .map((a) => a.querySelector('.mantine-Button-label') as HTMLElement)
          .filter((l) => l.scrollWidth > l.clientWidth + 1)
          .map((l) => l.textContent),
      ),
    ).toEqual([]);
    const code = card(page, 'android').locator('pre').first();
    expect((await code.boundingBox())!.width).toBeLessThanOrEqual(375);
    for (const locator of [
      buttons(page, 'windows').nth(0),
      buttons(page, 'windows').nth(1),
      card(page, 'linux').getByTestId('download-missing'),
      card(page, 'android').getByTestId('download-sha').first().locator('p'),
    ]) {
      const ratio = await contrast(page, locator);
      expect(ratio, `${scheme} ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(4.5);
    }
    await shot(page, `download-375-${scheme}`);
  }
});
