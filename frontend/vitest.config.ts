import { defineConfig } from 'vitest/config';
import react, { reactCompilerPreset } from '@vitejs/plugin-react';
import babel from '@rolldown/plugin-babel';

// Vitest runs the unit-test suite (Zustand stores, pure helpers, small
// hooks). Playwright specs live alongside under `e2e/**` — same
// `.spec.ts` extension — so we must exclude them explicitly or vitest
// would try to import them and choke on Playwright's `test()` API.
// Playwright is driven separately via `pnpm test:e2e` /
// `make test-e2e`.
//
// Default environment is `node` for speed; integration tests that
// render React components opt in via the per-file pragma:
//   // @vitest-environment jsdom
// at the top of the spec. setupFiles loads DOM matchers and React Testing
// Library only for those tests, while sharing state isolation with Node tests.
// The production build runs the React Compiler (vite.config.ts), which
// memoizes render output: a component that re-renders through unused state
// passes here and freezes in the shipped bundle. Opt modules in as they are
// verified under the compiler; the goal is to compile everything.
const compiledInTests = [
  /\/src\/components\/RelativeTime\.tsx$/,
];

export default defineConfig({
  plugins: [
    react(),
    babel({ presets: [reactCompilerPreset({ sources: (file) => compiledInTests.some((re) => re.test(file)) })] }),
  ],
  // The plugin UI contract tests read the published packages in ../sdk.
  server: { fs: { allow: ['.', '../sdk'] } },
  test: {
    environment: 'node',
    // Worker threads instead of forked processes: same isolation per
    // file, ~15% less CPU for the suite (matters on the shared CI
    // runner). `isolate: false` would cut CPU ~3x more but 57 tests
    // rely on fresh module state per file.
    pool: 'threads',
    setupFiles: ['./vitest.setup.ts'],
    // Node 25 ships a global `localStorage` that is unusable without
    // `--localstorage-file`, warns on first access, and shadows jsdom's
    // working Storage. Turn it off so jsdom's own implementation is used.
    execArgv: ['--no-experimental-webstorage'],
    // The shared CI runner is CPU-starved while Go and Playwright jobs
    // run alongside; the 5s default let healthy tests time out.
    testTimeout: 15_000,
    exclude: [
      '**/node_modules/**',
      '**/dist/**',
      'e2e/**',
      'component-e2e/**', // Isolated Playwright UI reviews, never unit tests.
      'perf/**', // Playwright interaction bench (`pnpm perf`).
    ],
    coverage: {
      // @vitest/coverage-v8 is a devDependency. The CI coverage
      // ratchet (spec/ci-coverage-ratchet) reads total.lines.pct from
      // the json-summary reporter's coverage-summary.json.
      provider: 'v8',
      reporter: ['text', 'json-summary'],
      reportsDirectory: './coverage',
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.test.{ts,tsx}',
        'src/**/*.d.ts',
        'src/main.tsx',
      ],
    },
  },
});
