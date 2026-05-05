// Loaded by every vitest run. Imports @testing-library/jest-dom so
// integration tests get matchers like `toBeInTheDocument`. The
// import is a no-op under the node environment because the matchers
// only register when `expect` is in scope, which it is for vitest.
import '@testing-library/jest-dom/vitest';
import { afterEach } from 'vitest';
import { cleanup, configure } from '@testing-library/react';

// Auto-cleanup the rendered DOM between tests so a stale tree from a
// previous case can't satisfy a `findByRole` query in the next one.
// Also fires under the node environment but degrades to a no-op when
// no document was rendered.
afterEach(() => {
  cleanup();
});

// CI runners are noticeably slower than dev hardware: cold-start the
// SessionDetail integration test (which re-imports ~30 modules per
// `renderSessionPage` call) routinely takes 2-3 s on the runner versus
// <500 ms locally. Lift the default `waitFor` timeout from 1 s to 5 s
// so the first slow render doesn't fail the suite. Per-call timeouts
// in the test bodies are honoured as overrides.
configure({ asyncUtilTimeout: 5000 });
