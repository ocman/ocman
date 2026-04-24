import { defineConfig } from 'vitest/config';

// Vitest runs the unit-test suite (Zustand stores, pure helpers, small
// hooks). Playwright specs live alongside under `e2e/**` — same
// `.spec.ts` extension — so we must exclude them explicitly or vitest
// would try to import them and choke on Playwright's `test()` API.
// Playwright is driven separately via `npm run test:e2e` /
// `make test-e2e`.
export default defineConfig({
  test: {
    exclude: [
      '**/node_modules/**',
      '**/dist/**',
      'e2e/**',
    ],
  },
});
