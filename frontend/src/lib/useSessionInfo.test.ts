import { describe, expect, it } from 'vitest';

// Module-level smoke test, mirroring useSessionChanges.test.ts. The
// project doesn't depend on @testing-library/react, so we don't render
// the hook here. The hook's logic (debounce, abort, EMPTY_INFO
// fallback) is exercised by manual UI verification in dev; this file
// just guarantees the module loads cleanly and exports the expected
// shape so a typo / circular import surfaces in CI.

describe('useSessionInfo module', () => {
  it('exports the hook function', async () => {
    const mod = await import('./useSessionInfo');
    expect(typeof mod.useSessionInfo).toBe('function');
  });
});
