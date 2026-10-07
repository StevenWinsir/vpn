import { defineConfig, devices } from '@playwright/test';

const baseURL = process.env.PLAYWRIGHT_BASE_URL;
if (
  process.env.E2E_ISOLATED !== 'private-postgres' ||
  !baseURL ||
  !/^http:\/\/127\.0\.0\.1:\d+$/.test(baseURL)
) {
  throw new Error(
    'Use scripts/test-node-catalog.py; node mutations require its private PostgreSQL fixture.',
  );
}

export default defineConfig({
  testDir: './tests',
  testMatch: ['admin-nodes.spec.ts', 'user-nodes.spec.ts'],
  outputDir: process.env.CATALOG_OUTPUTS,
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
      args: ['--no-proxy-server'],
      ...(process.env.PLAYWRIGHT_CHROME_PATH
        ? { executablePath: process.env.PLAYWRIGHT_CHROME_PATH }
        : {}),
    },
  },
});
