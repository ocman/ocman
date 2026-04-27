import { describe, expect, it } from 'vitest';

// Module-load smoke test, matching the project's existing pattern of
// not depending on @testing-library/react. The hook's logic
// (IntersectionObserver wiring, chunk reveal) is exercised in the
// browser via DiffView/RawDiffView; this file just guarantees the
// module loads and exports the expected shape.

describe('useInfiniteRows module', () => {
  it('exports the hook function', async () => {
    const mod = await import('./useInfiniteRows');
    expect(typeof mod.useInfiniteRows).toBe('function');
  });
});
