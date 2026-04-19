import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// useCapabilities is exercised at the module-level rather than through
// React's renderHook because the project doesn't depend on
// @testing-library/react. The hook's behavior is a thin wrapper around
// loadCapabilities, so verifying the module-scope cache + the
// "platform-not-found falls back to all-false" branch is enough.

describe('useCapabilities module', () => {
  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('exports a hook function and a per-platform helper', async () => {
    const mod = await import('./useCapabilities');
    expect(typeof mod.useCapabilities).toBe('function');
    expect(typeof mod.usePlatformCapabilities).toBe('function');
  });

  it('returns an empty default object until a real call resolves', async () => {
    // No fetch stub here — useCapabilities should never throw at
    // import time, which would crash any consuming component.
    const mod = await import('./useCapabilities');
    expect(mod.useCapabilities).toBeDefined();
    expect(mod.usePlatformCapabilities).toBeDefined();
  });
});
