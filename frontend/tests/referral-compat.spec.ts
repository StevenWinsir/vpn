import { expect, test, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';

// Mixed deployment of the referral programme (handoff_coupon.md 15.1, R3-02), the half that is
// the new website: against a backend that predates the programme and against the current one.
// It runs in scripts/test-release-rehearsal.py, whose API test assembly can switch itself into
// the answers of the older release (no referral_enabled in /meta, no referral routes, and a
// registration that carries referral_code refused as an unknown field, as the older decoder does).
// The other half, the old website against the new backend, is that script's own stage.
type Fixture = { api: string; control: string };

async function fixture(): Promise<Fixture> {
  const filename = process.env.REHEARSAL_FIXTURE;
  if (!filename?.startsWith('/tmp/vpn-release-')) throw new Error('Private rehearsal fixture required');
  return JSON.parse(await readFile(filename, 'utf8')) as Fixture;
}
const headers = (f: Fixture) => ({ 'X-Acceptance-Control': f.control });
async function legacy(on: boolean) {
  const f = await fixture();
  const response = await fetch(`${f.api}/fixture/legacy-backend?on=${on ? 1 : 0}`, { headers: headers(f) });
  expect(response.status).toBe(204);
}
async function mailCode(to: string) {
  const f = await fixture();
  for (let i = 0; i < 50; i++) {
    const response = await fetch(`${f.api}/fixture/mail?to=${encodeURIComponent(to)}`, {
      headers: headers(f),
    });
    if (response.ok) return ((await response.json()) as { code: string }).code;
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error('No registration code was delivered');
}
function watch(page: Page) {
  const registrations: Record<string, unknown>[] = [];
  const referralCalls: string[] = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().endsWith('/auth/register'))
      registrations.push(JSON.parse(request.postData() ?? '{}'));
    if (/\/me\/referrals?(\?|$)/.test(request.url())) referralCalls.push(request.url());
  });
  return { registrations, referralCalls };
}
async function register(page: Page, email: string, referral?: string) {
  await page.getByLabel('姓名 / 昵称').fill('兼容测试');
  await page.getByLabel('邮箱地址').fill(email);
  await page.getByRole('button', { name: '获取验证码' }).click();
  await expect(page.getByText('如果该邮箱可以注册')).toBeVisible();
  await page.getByLabel('邮箱验证码').fill(await mailCode(email));
  await page.locator('input[type="password"]').fill(`Compat-${randomUUID()}`);
  if (referral !== undefined) await page.getByLabel('推荐码（选填）').fill(referral);
  await page.getByRole('button', { name: '创建账户' }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

test.describe.configure({ mode: 'serial' });
test.afterAll(async () => {
  await legacy(false).catch(() => undefined);
});

test('the new website against a backend without the programme', async ({ page }) => {
  await legacy(true);
  const seen = watch(page);
  await page.goto('/register?ref=ABCD2345');
  await expect(page.getByLabel('姓名 / 昵称')).toBeVisible();
  // No field, so nothing to fill and nothing sent; the older decoder would refuse it.
  await expect(page.getByLabel('推荐码（选填）')).toHaveCount(0);
  await register(page, `compat-old-${randomUUID().slice(0, 10)}@gmail.com`);
  expect(seen.registrations).toHaveLength(1);
  expect(Object.keys(seen.registrations[0]).sort()).toEqual([
    'code',
    'email',
    'name',
    'password',
    'remember',
    'verification_id',
  ]);
  await expect(page.getByRole('heading', { name: '还没有开通套餐' })).toBeVisible();
  await expect(page.getByRole('link', { name: '推荐有礼' })).toHaveCount(0);
  await expect(page.getByTestId('referral-card')).toHaveCount(0);
  await page.goto('/referral');
  await expect(page.getByText('推荐有礼暂时不可用')).toBeVisible();
  expect(seen.referralCalls).toEqual([]);
});

test('the new website against the current backend: the link fills the field, which stays optional', async ({
  page,
}) => {
  await legacy(false);
  const seen = watch(page);
  await page.goto('/register?ref=ab2c-d3ef');
  await expect(page.getByLabel('推荐码（选填）')).toHaveValue('AB2CD3EF');
  // A code nobody owns is no reason for a message: the account is created all the same.
  await register(page, `compat-new-${randomUUID().slice(0, 10)}@gmail.com`);
  expect(seen.registrations[0].referral_code).toBe('AB2CD3EF');
  await expect(page.getByRole('heading', { name: '还没有开通套餐' })).toBeVisible();
  await expect(page.locator('.mantine-Alert-root')).toHaveCount(0);
  await expect(page.getByRole('link', { name: '推荐有礼' }).first()).toBeVisible();
  await page.getByRole('link', { name: '推荐有礼' }).first().click();
  await expect(page).toHaveURL(/\/referral$/);
  await expect(page.getByTestId('referral-code')).toHaveText(/^[2-9A-HJ-KM-NP-Z]{8}$/);
  await expect(page.getByTestId('referral-empty')).toBeVisible();
});
