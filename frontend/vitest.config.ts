import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// Vitest runs the unit-test suite (Zustand stores, pure helpers, small
// hooks). Playwright specs live alongside under `e2e/**` — same
// `.spec.ts` extension — so we must exclude them explicitly or vitest
// would try to import them and choke on Playwright's `test()` API.
// Playwright is driven separately via `npm run test:e2e` /
// `make test-e2e`.
//
// Default environment is `node` for speed; integration tests that
// render React components opt in via the per-file pragma:
//   // @vitest-environment jsdom
// at the top of the spec. setupFiles wires up @testing-library/jest-dom
// matchers; they are inert under the node env.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'node',
    setupFiles: ['./vitest.setup.ts'],
    exclude: [
      '**/node_modules/**',
      '**/dist/**',
      'e2e/**',
    ],
  },
});
