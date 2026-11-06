import { expect, test, type Browser, type Locator, type Page } from '@playwright/test';
import { mkdir, readFile } from 'node:fs/promises';
import { overflowing } from './layout';

// Browser acceptance for the console's "客户端下载" tab (handoff_download.md 7 and 9, DL2-01 …
// DL2-08), against the real API on the private PostgreSQL that scripts/test-payment-web.py starts.
// Every address is on downloads.example.test, which nothing ever fetches.
type Fixture = { password: string; emails: Record<'dlroot' | 'dla', string> };

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
  await page.screenshot({ path: `${dir}/${name}.png`, animations: 'disabled', fullPage: true });
}

async function signIn(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(email);
  await page.locator('input[type="password"]').fill(fixture.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

async function second(browser: Browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  await signIn(page, fixture.emails.dlroot);
  await page.goto('/admin/console/downloads');
  return { context, page };
}

const sha = (n: number) => n.toString(16).padStart(2, '0').repeat(32);
// Toasts stack for a few seconds; the newest one is the answer to the last action.
const toast = (page: Page, text: string) =>
  page.getByTestId('toast').filter({ hasText: text }).last();
const editor = (page: Page) => page.getByRole('dialog');
const group = (page: Page, platform: string) => page.getByTestId(`download-group-${platform}`);
const row = (page: Page, variant: string) =>
  page.locator(`[data-testid="download-row"][data-variant="${variant}"]`);

async function choose(page: Page, dialog: Locator, field: '架构' | '格式', option: string) {
  await dialog.getByLabel(field, { exact: true }).click();
  await page.getByRole('option', { name: option, exact: true }).click();
}

type Entry = {
  platform: string;
  name: string;
  version: string;
  url: string;
  sha?: string;
  size?: string;
  cert?: string;
  bundle?: string;
};
async function add(page: Page, entry: Entry) {
  await page.getByRole('button', { name: `添加 ${entry.name}` }).click();
  const dialog = editor(page);
  await expect(dialog).toContainText(`添加 ${entry.name} 下载`);
  await dialog.getByLabel('版本号').fill(entry.version);
  await dialog.getByLabel('下载地址').fill(entry.url);
  if (entry.sha) await dialog.getByLabel('SHA-256', { exact: true }).fill(entry.sha);
  if (entry.size) await dialog.getByLabel('文件大小（字节）').fill(entry.size);
  if (entry.cert) await dialog.getByLabel('签名证书 SHA-256').fill(entry.cert);
  if (entry.bundle) await dialog.getByLabel('Bundle ID').fill(entry.bundle);
  await dialog.getByRole('button', { name: '保存' }).click();
  await expect(
    page
      .getByTestId('toast')
      .filter({ hasText: '已添加' })
      .filter({ hasText: `${entry.name} · ` }),
  ).toBeVisible();
  await expect(dialog).toBeHidden();
}

test.describe.configure({ mode: 'serial' });

test('a user who is not an administrator cannot open the tab', async ({ page }) => {
  await signIn(page, fixture.emails.dla);
  await page.goto('/admin/console/downloads');
  await expect(page.getByRole('alert').filter({ hasText: '无权访问' })).toBeVisible();
  await expect(page.getByTestId('download-group-windows')).toHaveCount(0);
});

test('client downloads: add per platform, choices, checks, edit, conflict, publish, delete', async ({
  page,
  browser,
}) => {
  await signIn(page, fixture.emails.dlroot);
  await page.goto('/admin/console/downloads');

  // DL2-01: the tab, five groups in the page order, all empty.
  await expect(page.getByRole('link', { name: '客户端下载' })).toHaveClass(/active/);
  await expect(page.getByTestId('downloads-notice')).toContainText('30 秒');
  const groups = page.locator('[data-testid^="download-group-"]');
  await expect(groups).toHaveCount(5);
  expect(await groups.evaluateAll((all) => all.map((g) => g.getAttribute('data-testid')))).toEqual(
    ['windows', 'macos', 'android', 'ios', 'linux'].map((p) => `download-group-${p}`),
  );
  await expect(page.getByTestId('download-empty')).toHaveCount(5);
  await shot(page, 'downloads-empty');

  // DL2-04 and DL2-07 in the Windows editor: the live size in MB, checks before saving.
  await page.getByRole('button', { name: '添加 Windows' }).click();
  let dialog = editor(page);
  await expect(dialog.getByLabel('架构', { exact: true })).toHaveValue('x64');
  await expect(dialog.getByLabel('格式', { exact: true })).toHaveValue('安装程序 .exe');
  await dialog.getByLabel('文件大小（字节）').fill('48128121');
  await expect(dialog).toContainText('约 45.9 MB');
  await dialog.getByLabel('版本号').fill('latest');
  await dialog.getByLabel('下载地址').fill('http://downloads.example.test/AsterLink-Setup.exe');
  await dialog.getByRole('button', { name: '保存' }).click();
  await expect(dialog).toContainText('版本号格式应为 1.2.3');
  await dialog.getByLabel('版本号').fill('1.2.3');
  await expect(dialog).toContainText('下载地址必须以 https:// 开头');
  await dialog.getByLabel('下载地址').fill('https://downloads.example.test/AsterLink-Setup.dmg');
  await expect(dialog).toContainText('文件名须以 .exe 结尾');
  await dialog
    .getByLabel('下载地址')
    .fill('https://downloads.example.test/AsterLink-Setup-x64.exe');
  await expect(dialog).toContainText('SHA-256 须为 64 位十六进制');
  // Upper case with colons, as signing tools print it: saved normalised.
  await dialog
    .getByLabel('SHA-256', { exact: true })
    .fill(sha(0xab).toUpperCase().match(/../g)!.join(':'));
  await dialog.getByRole('button', { name: '保存' }).click();
  await expect(toast(page, 'Windows · x64 · 安装程序 .exe')).toContainText('已添加');
  await expect(dialog).toBeHidden();

  // DL2-02: one entry per platform, each listed at once and not yet published.
  await add(page, {
    platform: 'macos',
    name: 'macOS',
    version: '1.2.3',
    url: 'https://downloads.example.test/AsterLink-1.2.3-arm64.dmg',
    sha: sha(0x02),
    size: '73400320',
  });
  await add(page, {
    platform: 'android',
    name: 'Android',
    version: '1.2.3',
    url: 'https://downloads.example.test/AsterLink-1.2.3-android-arm64-v8a.apk',
    sha: sha(0x03),
    size: '48128121',
    cert: sha(0xf5),
  });
  await add(page, {
    platform: 'ios',
    name: 'iOS',
    version: '1.2.3',
    url: 'https://downloads.example.test/AsterLink-1.2.3.ipa',
    sha: sha(0x04),
    bundle: 'com.example.asterlink',
  });
  await add(page, {
    platform: 'linux',
    name: 'Linux',
    version: '1.2.3',
    url: 'https://downloads.example.test/AsterLink-1.2.3-x86_64.AppImage',
    sha: sha(0x05),
  });
  for (const variant of [
    'windows/x64/exe',
    'macos/arm64/dmg',
    'android/arm64-v8a/apk',
    'ios/arm64/ipa',
    'linux/x64/appimage',
  ]) {
    await expect(row(page, variant)).toBeVisible();
    await expect(row(page, variant)).toContainText('未上架');
  }
  await expect(page.getByTestId('download-empty')).toHaveCount(0);
  await expect(page.getByTestId('download-count')).toContainText('共 5 条');

  // DL2-07: the short hash shows the full one on hover; the address opens in a new tab.
  const win = row(page, 'windows/x64/exe');
  await expect(win.getByTestId('download-sha')).toHaveText(`${sha(0xab).slice(0, 8)}…`);
  await expect(win.getByTestId('download-sha')).toHaveAttribute('title', sha(0xab));
  await expect(win).toContainText('45.9 MB');
  const open = win.getByRole('link', { name: /在新标签打开/ });
  await expect(open).toHaveAttribute(
    'href',
    'https://downloads.example.test/AsterLink-Setup-x64.exe',
  );
  await expect(open).toHaveAttribute('target', '_blank');
  await expect(open).toHaveAttribute('rel', /noopener/);

  // DL2-03: a taken combination is not offered again; store formats need no hash or size.
  await page.getByRole('button', { name: '添加 Windows' }).click();
  dialog = editor(page);
  await expect(dialog.getByLabel('格式', { exact: true })).toHaveValue('MSI 安装包 .msi');
  await dialog.getByLabel('格式', { exact: true }).click();
  await expect(page.getByRole('option', { name: '安装程序 .exe（已添加）' })).toHaveAttribute(
    'data-combobox-disabled',
    'true',
  );
  await page.getByRole('option', { name: 'MSI 安装包 .msi', exact: true }).click();
  // DL2-04: the server's verdict lands under its field. "%ZZ" is a broken escape the browser
  // keeps but the server's parser refuses.
  await dialog.getByLabel('版本号').fill('1.2.3');
  await dialog.getByLabel('下载地址').fill('https://downloads.example.test/AsterLink-%ZZ.msi');
  await dialog.getByLabel('SHA-256', { exact: true }).fill(sha(0x06));
  await dialog.getByRole('button', { name: '保存' }).click();
  await expect(dialog.getByText('下载地址格式不正确')).toBeVisible();
  await dialog.getByLabel('下载地址').fill('https://downloads.example.test/AsterLink-x64.msi');
  await expect(dialog.getByText('下载地址格式不正确')).toHaveCount(0);
  await dialog.getByRole('button', { name: '取消' }).click();

  await page.getByRole('button', { name: '添加 iOS' }).click();
  dialog = editor(page);
  await expect(dialog.getByLabel('格式', { exact: true })).toHaveValue('App Store');
  await expect(dialog.getByLabel('SHA-256', { exact: true })).toHaveCount(0);
  await expect(dialog.getByLabel('文件大小（字节）')).toHaveCount(0);
  await expect(dialog.getByLabel('Bundle ID')).toHaveCount(0);
  await expect(dialog).toContainText('https://apps.apple.com');
  await choose(page, dialog, '格式', 'TestFlight');
  await expect(dialog).toContainText('https://testflight.apple.com');
  await dialog.getByLabel('版本号').fill('1.2.3');
  await dialog.getByLabel('下载地址').fill('https://apps.apple.com/app/asterlink/id123');
  await dialog.getByRole('button', { name: '保存' }).click();
  await expect(dialog).toContainText('该格式只能填写 https://testflight.apple.com 的页面地址');
  await dialog.getByLabel('下载地址').fill('https://testflight.apple.com/join/AbCdEf');
  await dialog.getByRole('button', { name: '保存' }).click();
  await expect(toast(page, 'iOS · arm64 · TestFlight')).toContainText('已添加');
  await expect(row(page, 'ios/arm64/testflight')).toBeVisible();
  await shot(page, 'downloads-list');

  // DL2-05: edit, then a second administrator saves first and this form says so.
  await page.getByRole('button', { name: '编辑 Windows · x64 · 安装程序 .exe' }).click();
  dialog = editor(page);
  await expect(dialog.getByLabel('平台')).toBeDisabled();
  await dialog.getByLabel('版本号').fill('1.2.4');
  await dialog.getByRole('button', { name: '保存' }).click();
  await expect(toast(page, '已保存')).toBeVisible();
  await expect(win).toContainText('1.2.4');

  await page.getByRole('button', { name: '编辑 Windows · x64 · 安装程序 .exe' }).click();
  dialog = editor(page);
  await dialog.getByLabel('版本号').fill('1.2.6');
  const other = await second(browser);
  await other.page.getByRole('button', { name: '编辑 Windows · x64 · 安装程序 .exe' }).click();
  await editor(other.page).getByLabel('版本号').fill('1.2.5');
  await editor(other.page).getByRole('button', { name: '保存' }).click();
  await expect(toast(other.page, '已保存')).toBeVisible();
  await other.context.close();
  await dialog.getByRole('button', { name: '保存' }).click();
  await expect(dialog.getByTestId('download-conflict')).toContainText(
    '已被其他管理员修改，请刷新后重试',
  );
  await dialog.getByRole('button', { name: '刷新' }).click();
  await expect(dialog.getByLabel('版本号')).toHaveValue('1.2.5');
  await expect(dialog.getByTestId('download-conflict')).toHaveCount(0);
  await dialog.getByLabel('版本号').fill('1.2.6');
  await dialog.getByRole('button', { name: '保存' }).click();
  await expect(toast(page, '已保存')).toBeVisible();
  await expect(win).toContainText('1.2.6');

  // DL2-06: publish and withdraw; the public list follows (without the address).
  await win.getByRole('switch').click();
  await expect(toast(page, '已上架')).toContainText('Windows · x64');
  await expect(win).toContainText('已上架');
  const published = await page.request.get('/api/v1/downloads');
  const body = await published.text();
  expect(body).toContain('"platform":"windows"');
  expect(body).not.toContain('downloads.example.test');
  await win.getByRole('switch').click();
  await expect(toast(page, '已下架')).toBeVisible();
  await expect(win).toContainText('未上架');

  await page.getByRole('button', { name: '删除 Linux · x64 · AppImage' }).click();
  const confirm = page.getByRole('dialog', { name: '删除下载条目' });
  await expect(confirm).toContainText('Linux · x64 · AppImage');
  await expect(confirm).toContainText('1.2.3');
  await confirm.getByRole('button', { name: '确认删除' }).click();
  await expect(toast(page, '已删除')).toBeVisible();
  await expect(row(page, 'linux/x64/appimage')).toHaveCount(0);
  await expect(group(page, 'linux').getByTestId('download-empty')).toBeVisible();
});

test('client downloads: light and dark, 1280 and 768 wide, nothing sticks out', async ({
  page,
}) => {
  await signIn(page, fixture.emails.dlroot);
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    for (const width of [1280, 768]) {
      await page.setViewportSize({ width, height: 1000 });
      await page.goto('/admin/console/downloads');
      await expect(row(page, 'windows/x64/exe')).toBeVisible();
      expect(await overflowing(page)).toEqual([]);
      await shot(page, `downloads-${scheme}-${width}`);
    }
  }
});

// Nothing this spec added stays behind for the download page specs that run later.
test.afterAll(async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await signIn(page, fixture.emails.dlroot);
  await page.goto('/admin/console/downloads');
  const remove = page.getByRole('button', { name: /^删除 / });
  await expect(page.getByTestId('download-count')).toBeVisible();
  while ((await remove.count()) > 0) {
    await remove.first().click();
    await page
      .getByRole('dialog', { name: '删除下载条目' })
      .getByRole('button', { name: '确认删除' })
      .click();
    await expect(page.getByRole('dialog', { name: '删除下载条目' })).toBeHidden();
  }
  await expect(page.getByTestId('download-count')).toContainText('共 0 条');
  await context.close();
});
