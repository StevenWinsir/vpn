import { defineConfig, devices } from '@playwright/test';
import { crispUnreachable } from './tests/crisp-hosts';

const baseURL = process.env.PLAYWRIGHT_BASE_URL;
if (
  process.env.E2E_ISOLATED !== 'private-postgres' ||
  !baseURL ||
  !/^http:\/\/127\.0\.0\.1:\d+$/.test(baseURL)
) {
  throw new Error(
    'Use scripts/test-payment-web.py; payment writes require its private PostgreSQL and gateway fixture.',
  );
}

export default defineConfig({
  testDir: './tests',
  testMatch: [
    'payment.spec.ts',
    'account.spec.ts',
    'admin-console.spec.ts',
    'admin-console-2.spec.ts',
    'admin-console-plans.spec.ts',
    'shop.spec.ts',
    'referral.spec.ts',
    'ui-copy.spec.ts',
    'theme.spec.ts',
    'home.spec.ts',
    'support-widget.spec.ts',
  ],
  outputDir: process.env.PAYMENT_OUTPUTS,
  workers: 1,
  retries: 0,
  timeout: 120000,
  expect: { timeout: 20000 },
  reporter: [['list']],
  use: {
    ...devices['Desktop Chrome'],
    baseURL,
    viewport: { width: 1440, height: 1000 },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    launchOptions: {
      // No browser of any spec reaches the real Crisp (handoff_ui.md 11, round 4): its hosts do not
      // resolve, so every page also runs with the support chat unreachable. Request interception
      // (support-widget.spec) happens before name resolution and is not affected.
      args: ['--no-proxy-server', crispUnreachable],
      ...(process.env.PLAYWRIGHT_CHROME_PATH
        ? { executablePath: process.env.PLAYWRIGHT_CHROME_PATH }
        : {}),
    },
  },
});
