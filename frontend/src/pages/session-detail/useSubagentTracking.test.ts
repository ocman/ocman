// @vitest-environment jsdom
//
// useSubagentTracking owns a bounded per-message token map. The cap
// (MAX_SUBAGENT_TOKEN_ENTRIES) protects against unbounded growth
// during long subagent runs.

import { describe, expect, it } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useSubagentTracking } from './useSubagentTracking';

describe('useSubagentTracking', () => {
  it('exposes a stable setSubagentTokens identity across renders', () => {
    // Regression: a previous version returned a fresh function
    // every render. Listing the setter in `useSessionStatus`'s TPS
    // effect deps then caused a render loop ("Maximum update depth
    // exceeded") during active streaming.
    const { result, rerender } = renderHook(() => useSubagentTracking([], 's1'));
    const first = result.current.setSubagentTokens;
    rerender();
    expect(result.current.setSubagentTokens).toBe(first);
    rerender();
    expect(result.current.setSubagentTokens).toBe(first);
  });

  it('trims subagent token entries past the cap', () => {
    const { result } = renderHook(() => useSubagentTracking([], 's1'));

    // Push more entries than the cap (256) and confirm the cap is
    // enforced. We use the functional updater so the cap helper is
    // exercised end-to-end. The state update is wrapped in act() so
    // React commits before we read back.
    const N = 300;
    act(() => {
      result.current.setSubagentTokens(() => {
        const map = new Map<string, { output: number; created: number }>();
        for (let i = 0; i < N; i++) {
          map.set(`m${i}`, { output: i, created: Date.now() + i });
        }
        return map;
      });
    });

    // The trimmer keeps the most recently inserted entries
    // (insertion order = chronological in this test), so the
    // lowest-id entries should be evicted.
    expect(result.current.subagentTokens.size).toBeLessThanOrEqual(256);
    expect(result.current.subagentTokens.has('m0')).toBe(false);
    expect(result.current.subagentTokens.has(`m${N - 1}`)).toBe(true);
  });
});
