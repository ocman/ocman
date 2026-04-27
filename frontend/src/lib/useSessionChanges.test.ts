import { describe, expect, it } from 'vitest';

// Module-level smoke tests — the project doesn't depend on
// @testing-library/react, so we don't render the hook. The
// hook's logic (debounce, abort) is straightforward enough that the
// integration is covered by the visible UI behavior in
// SessionDetail; this file just guarantees the module loads
// cleanly and exports the expected shape.

describe('useSessionChanges module', () => {
  it('exports the hook function', async () => {
    const mod = await import('./useSessionChanges');
    expect(typeof mod.useSessionChanges).toBe('function');
  });
});
