// Playwright end-to-end config. Run through tools/e2e.sh (./dev e2e), which
// starts a real `bank serve` with BANK_DEV_TEST_LOGIN=1 and sets E2E_BASE_URL.
import { defineConfig, devices } from '@playwright/test';

const chromium = process.env['E2E_CHROMIUM'];

export default defineConfig({
  testDir: 'e2e',
  retries: 0,
  forbidOnly: !!process.env['CI'],
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: [['list'], ['html', { outputFolder: 'playwright-report', open: 'never' }]],
  outputDir: 'test-results',
  use: {
    baseURL: process.env['E2E_BASE_URL'] ?? 'http://localhost:8080',
    trace: 'retain-on-failure',
    // The PWA service worker would cache the shell between specs; flows are
    // tested against the network. Offline behaviour has unit tests.
    serviceWorkers: 'block',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        ...(chromium ? { launchOptions: { executablePath: chromium } } : {}),
      },
    },
  ],
});
