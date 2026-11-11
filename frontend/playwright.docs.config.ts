import { createHash } from 'node:crypto';
import { defineConfig } from '@playwright/test';

// Hugo only. No application backend, agent process or writable database.
const port = 30_000 + createHash('sha256').update(import.meta.dirname).digest().readUInt16BE(0) % 10_000;
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: './e2e-docs',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  use: { baseURL, browserName: 'chromium', screenshot: 'only-on-failure' },
  projects: [
    { name: 'desktop', use: { viewport: { width: 1440, height: 1000 }, reducedMotion: 'no-preference' } },
    { name: 'mobile', use: { viewport: { width: 390, height: 844 }, isMobile: true, reducedMotion: 'no-preference' } },
    { name: 'short-desktop', use: { viewport: { width: 1440, height: 700 }, reducedMotion: 'no-preference' } },
    { name: 'reduced-motion', use: { viewport: { width: 1440, height: 1000 }, reducedMotion: 'reduce' } },
    { name: 'no-javascript', use: { viewport: { width: 1440, height: 1000 }, javaScriptEnabled: false } },
  ],
  webServer: {
    command: `hugo server --source ../site --bind 127.0.0.1 --port ${port} --baseURL ${baseURL} --disableLiveReload`,
    url: baseURL,
    reuseExistingServer: false,
    timeout: 30_000,
  },
});
