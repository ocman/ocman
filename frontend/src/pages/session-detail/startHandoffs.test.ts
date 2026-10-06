// @vitest-environment jsdom
import { renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { startHandoffs, useStartHandoff } from './startHandoffs';

describe('useStartHandoff', () => {
  it('shows the handoff until the first message, then drops it for good', () => {
    const handoff = { prompt: 'Fix login', steps: { prompt: 'done' as const } };
    startHandoffs.set('s1', handoff);
    const { result, rerender } = renderHook(({ count }) => useStartHandoff('s1', count), { initialProps: { count: 0 } });
    expect(result.current).toBe(handoff);
    rerender({ count: 1 });
    expect(result.current).toBeUndefined();
    expect(startHandoffs.has('s1')).toBe(false);
    // An emptied thread (e.g. a dismissed message) does not bring it back.
    rerender({ count: 0 });
    expect(result.current).toBeUndefined();
  });

  it('has nothing for an unknown or missing session', () => {
    expect(renderHook(() => useStartHandoff('other', 0)).result.current).toBeUndefined();
    expect(renderHook(() => useStartHandoff(undefined, 0)).result.current).toBeUndefined();
  });
});
