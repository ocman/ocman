import { describe, expect, it } from 'vitest';

// Module-load smoke test, matching the pattern used by
// useSessionChanges.test.ts and useCapabilities.test.ts. The project
// doesn't depend on @testing-library/react, so we avoid renderHook.

describe('useWorkingTreeDiff module', () => {
  it('exports the hook function', async () => {
    const mod = await import('./useWorkingTreeDiff');
    expect(typeof mod.useWorkingTreeDiff).toBe('function');
  });
});
