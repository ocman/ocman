import { defineConfig, devices } from '@playwright/test';

/**
 * Interaction bench (`pnpm perf`), separate from the e2e suite. Serves the
 * profiling build from perf/dist on its own port so it never reuses a dev
 * server (or a real backend) on :8228.
 */
const port = Number(process.env.PERF_PORT ?? 8338);
// PERF_DIST serves another build (e.g. a baseline) for A/B runs.
const dist = process.env.PERF_DIST ?? 'perf/dist';

export default defineConfig({
  testDir: './perf',
  workers: 1,
  reporter: 'list',
  use: { ...devices['Desktop Chrome'], baseURL: `http://127.0.0.1:${port}` },
  webServer: {
    command: `pnpm exec vite preview --outDir ${dist} --port ${port} --strictPort`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: false,
    timeout: 30_000,
  },
});
