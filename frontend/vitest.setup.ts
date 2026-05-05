// Loaded by every vitest run. Imports @testing-library/jest-dom so
// integration tests get matchers like `toBeInTheDocument`. The
// import is a no-op under the node environment because the matchers
// only register when `expect` is in scope, which it is for vitest.
import '@testing-library/jest-dom/vitest';
import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';

// Auto-cleanup the rendered DOM between tests so a stale tree from a
// previous case can't satisfy a `findByRole` query in the next one.
// Also fires under the node environment but degrades to a no-op when
// no document was rendered.
afterEach(() => {
  cleanup();
});
