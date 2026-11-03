import { expect, test, type Page } from '@playwright/test';
import { readFile } from 'node:fs/promises';

// Mixed deployments of mid-period upgrades (handoff_plans.md 14.1, round 3: R3-01, R3-02).
// scripts/test-release-rehearsal.py runs this spec three times against real API binaries over
// one private database, each run naming its case in a private file:
//   old-website  the website of dbe75ca (the release before upgrades, built from its own tree)
//                against the current backend: other plans still read "cannot change", nothing
//                asks for an upgrade route, an upgrade order is listed without a page error;
//   old-backend  this website against the real dbe75ca binary (no plan_upgrade_supported):
//                the local rule, the wording without "升级", no upgrade request;
//   current      this website against the re-upgraded binary: the upgrade card and dialog show
//                exactly the server's quote (an order paid on dbe75ca credited by its fallback window).
type Account = { email: string; password: string };
type PlanRef = { id: string; name: string };
type Compat = {
  mode: 'old-website' | 'old-backend' | 'current';
  user: Account;
  current: PlanRef;
  blocked: PlanRef[];
  upgrade?: PlanRef & { amount_cents: number; credit_cents: number };
};

let compat: Compat;
test.beforeAll(async () => {
  const filename = process.env.REHEARSAL_PLANS_COMPAT;
  if (!filename?.startsWith('/tmp/vpn-release-'))
    throw new Error('Private rehearsal plans file required');
  compat = JSON.parse(await readFile(filename, 'utf8')) as Compat;
});

test.describe.configure({ mode: 'serial', timeout: 120_000 });

const cents = (c: number) => `¥${(c / 100).toFixed(2)}`;
const card = (page: Page, id: string) =>
  page.locator(`[data-testid="plan-card"][data-plan="${id}"]`);

// Every request to an upgrade route and every uncaught error of the page.
function watch(page: Page) {
  const upgradeCalls: string[] = [];
  const errors: string[] = [];
  page.on('request', (request) => {
    if (request.url().includes('/api/v1/payments/upgrade-')) upgradeCalls.push(request.url());
  });
  page.on('pageerror', (error) => errors.push(error.message));
  return { upgradeCalls, errors };
}

async function signIn(page: Page) {
  await page.goto('/login');
  await page.getByLabel('邮箱地址').fill(compat.user.email);
  await page.locator('input[type="password"]').fill(compat.user.password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}

test('the plans page and the order list on this pairing', async ({ page }) => {
  const seen = watch(page);
  await signIn(page);
  if (compat.mode === 'current') {
    const options = page.waitForResponse((r) => r.url().includes('/payments/upgrade-options'));
    await page.goto('/plans');
    expect((await options).status()).toBe(200);
  } else {
    await page.goto('/plans');
  }
  await expect(card(page, compat.current.id)).toBeVisible();
  await expect(
    card(page, compat.current.id).getByRole('button', {
      name: `续费 ${compat.current.name}`,
      exact: true,
    }),
  ).toBeEnabled();

  for (const plan of compat.blocked) {
    const button = card(page, plan.id).getByRole('button', {
      name: `购买 ${plan.name}`,
      exact: true,
    });
    await expect(button).toBeDisabled();
    if (compat.mode === 'old-website') {
      // The release before upgrades, word for word.
      await expect(card(page, plan.id)).toContainText(
        '当前套餐有效期内不能更换套餐，到期后可购买。',
      );
    } else {
      await expect(button).toHaveText('到期后可购买');
    }
    if (compat.mode === 'old-backend') {
      await expect(card(page, plan.id).getByTestId('closed-hint')).toHaveText(
        '当前套餐有效期内不能更换，到期后可购买。',
      );
    }
  }

  if (compat.mode === 'old-backend') {
    await expect(page.getByTestId('upgrade-button')).toHaveCount(0);
    await expect(page.getByTestId('change-rules')).toContainText('有效期内不能更换为其他套餐');
    await expect(page.getByTestId('change-rules')).not.toContainText('升级');
  }

  if (compat.mode === 'current') {
    const upgrade = compat.upgrade!;
    await expect(card(page, upgrade.id).getByTestId('upgrade-hint')).toContainText(
      `补差价约 ${cents(upgrade.amount_cents)}，到期日不变`,
    );
    const quoted = page.waitForResponse(
      (r) => r.url().includes('/payments/upgrade-quote') && r.request().method() === 'POST',
    );
    await page.getByRole('button', { name: `升级到 ${upgrade.name}`, exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    const quote = (await (await quoted).json()).quote as {
      amount_cents: number;
      credit_cents: number;
    };
    expect([quote.amount_cents, quote.credit_cents]).toEqual([
      upgrade.amount_cents,
      upgrade.credit_cents,
    ]);
    await expect(dialog.getByLabel('应付金额', { exact: true })).toContainText(
      cents(upgrade.amount_cents),
    );
    await expect(dialog).toContainText(cents(upgrade.credit_cents));
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
  } else {
    // Nothing on an older half of the deployment asks for an upgrade route.
    await page.waitForTimeout(1_500);
    expect(seen.upgradeCalls).toEqual([]);
  }

  // The order list renders whatever the other half wrote (an upgrade order on the old website
  // reads as the plan with 0 days: display only, handoff_plans.md 14.1).
  await page.goto('/orders');
  await expect(page.getByText(compat.current.name).first()).toBeVisible();
  if (compat.mode === 'old-website') await expect(page.getByText('/ 0 天').first()).toBeVisible();
  expect(seen.errors).toEqual([]);
});
