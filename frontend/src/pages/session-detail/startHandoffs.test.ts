// @vitest-environment jsdom
import { renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { startHandoffs, useStartHandoff } from './startHandoffs';

describe('useStartHandoff', () => {
  it('shows the handoff until the first message, then drops it for good', () => {
    const handoff = { prompt: 'Fix login', steps: { prompt: 'done' as const } };
    startHandoffs.set('s1', handoff);
    const { result, rerender } = renderHook(({ count }) => useStartHandoff('s1', 's1', count), { initialProps: { count: 0 } });
    expect(result.current).toBe(handoff);
    rerender({ count: 1 });
    expect(result.current).toBeUndefined();
    expect(startHandoffs.has('s1')).toBe(false);
    // An emptied thread (e.g. a dismissed message) does not bring it back.
    rerender({ count: 0 });
    expect(result.current).toBeUndefined();
  });

  it('ignores the previous session\'s messages while the view switches', () => {
    const handoff = { prompt: 'Fix login', steps: {} };
    startHandoffs.set('s2', handoff);
    const { result, rerender } = renderHook(({ view, count }) => useStartHandoff('s2', view, count), {
      initialProps: { view: 'old', count: 5 },
    });
    expect(result.current).toBe(handoff);
    expect(startHandoffs.has('s2')).toBe(true);
    rerender({ view: 's2', count: 0 });
    expect(result.current).toBe(handoff);
  });

  it('has nothing for an unknown or missing session', () => {
    expect(renderHook(() => useStartHandoff('other', 'other', 0)).result.current).toBeUndefined();
    expect(renderHook(() => useStartHandoff(undefined, undefined, 0)).result.current).toBeUndefined();
  });
});
