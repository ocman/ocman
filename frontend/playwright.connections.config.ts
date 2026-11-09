import { createHash } from 'node:crypto';
import { defineConfig, devices } from '@playwright/test';

// Review the real components without a backend or native agent credentials.
// This entry is outside the production bundle and normal application e2e suite.
const port = 30_000 + createHash('sha256').update(import.meta.dirname).digest().readUInt16BE(0) % 10_000;
export default defineConfig({
  testDir: './component-e2e',
  testMatch: 'platform-connections.spec.ts',
  fullyParallel: true,
  workers: process.env.CI ? 2 : undefined,
  use: { baseURL: `http://127.0.0.1:${port}`, trace: 'retain-on-failure', screenshot: 'only-on-failure' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: `pnpm exec vite --host 127.0.0.1 --port ${port} --strictPort`,
    url: `http://127.0.0.1:${port}/component-e2e/platform-connections.html`,
    reuseExistingServer: false,
  },
});
