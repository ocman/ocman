import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright e2e configuration for ocman frontend.
 *
 * Tests run against a live dev server (Vite on :8228, proxying /api to :8229).
 * Set E2E_BASE_URL to override (e.g. against a production build on a different
 * host). Set E2E_BACKEND_URL to point at a different backend.
 *
 * By default the webServer block starts `vite preview` (requires a built
 * frontend) so that tests run without a full npm-run-dev in CI. For local
 * interactive use, start the dev server yourself and set
 * E2E_NO_WEBSERVER=1 to skip the auto-start.
 */

const baseURL = process.env.E2E_BASE_URL ?? 'http://localhost:8228';
const noWebServer = process.env.E2E_NO_WEBSERVER === '1';
const useDevServer = process.env.E2E_USE_DEV_SERVER === '1';

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    // WebKit is excluded from CI (only Chromium is installed there).
    // Run locally with `pnpm exec playwright test --project=webkit`.
    ...(!process.env.CI ? [{
      name: 'webkit',
      use: { ...devices['Desktop Safari'] },
    }] : []),
  ],

  webServer: noWebServer
    ? undefined
    : {
        command: useDevServer
          ? 'pnpm dev -- --host 127.0.0.1 --port 8228'
          : 'pnpm preview',
        url: baseURL,
        reuseExistingServer: true,
        timeout: 30_000,
      },
});
