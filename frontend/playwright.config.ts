import { defineConfig, devices } from '@playwright/test';
const baseURL = process.env.PLAYWRIGHT_BASE_URL;
if (
  process.env.E2E_ISOLATED !== 'private-postgres' ||
  !baseURL ||
  !/^http:\/\/127\.0\.0\.1:\d+$/.test(baseURL)
) {
  throw new Error('Use python3 scripts/test-release-rehearsal.py --output <directory>; browser writes require its private PostgreSQL fixture.');
}
export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 180000,
  expect: { timeout: 30000 },
  reporter: [['list']],
  use: {
    baseURL,
    actionTimeout: 15000,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    launchOptions: {
      args: ['--no-proxy-server'],
      ...(process.env.PLAYWRIGHT_CHROME_PATH
        ? { executablePath: process.env.PLAYWRIGHT_CHROME_PATH }
        : {}),
    },
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1000 } },
    },
  ],
});
