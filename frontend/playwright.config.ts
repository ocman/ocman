import { createHash } from 'node:crypto';
import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright e2e configuration for ocman frontend.
 *
 * Every spec mocks /api through the shared fixtures, so tests are isolated
 * and run fully parallel. E2E_WORKERS overrides the worker count.
 *
 * By default the webServer block starts `vite preview` (requires a built
 * frontend) on a port derived from this checkout's path, so worktrees never
 * share a server or test another checkout's bundle. Set E2E_PORT to pick the
 * port, E2E_USE_DEV_SERVER=1 to start the Vite dev server instead, or
 * E2E_NO_WEBSERVER=1 with E2E_BASE_URL to test a server you started.
 */

const checkoutPort = 20_000 + createHash('sha256').update(import.meta.dirname).digest().readUInt16BE(0) % 10_000;
const port = Number(process.env.E2E_PORT) || checkoutPort;
const baseURL = process.env.E2E_BASE_URL ?? `http://127.0.0.1:${port}`;
const noWebServer = process.env.E2E_NO_WEBSERVER === '1';
const useDevServer = process.env.E2E_USE_DEV_SERVER === '1';
const workers = process.env.E2E_WORKERS ?? (process.env.CI ? 4 : undefined);

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: workers && /^\d+$/.test(workers) ? Number(workers) : workers,
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
      use: {
        ...devices['Desktop Safari'],
        // Block the PWA service worker so Playwright's page.route()
        // intercepts fetch() calls reliably. Without this, WebKit
        // routes requests through the service worker process, which
        // bypasses Playwright's network interception layer for fetches
        // triggered by React event handlers (user gestures).
        serviceWorkers: 'block' as const,
      },
    }] : []),
  ],

  webServer: noWebServer
    ? undefined
    : {
        command: `pnpm exec vite ${useDevServer ? '' : 'preview '}--host 127.0.0.1 --port ${port} --strictPort`,
        url: baseURL,
        // Never test whatever already listens on the port; it may be another checkout.
        reuseExistingServer: false,
        timeout: 30_000,
      },
});
