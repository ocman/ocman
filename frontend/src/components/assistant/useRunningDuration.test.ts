// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useRunningDuration } from './useRunningDuration';

describe('useRunningDuration', () => {
  afterEach(() => vi.useRealTimers());

  it('ticks while running and snaps back to the reported value when idle', () => {
    vi.useFakeTimers();
    const { result, rerender } = renderHook(
      ({ base, running }: { base: number; running: boolean }) => useRunningDuration(base, running),
      { initialProps: { base: 1000, running: false } },
    );
    expect(result.current).toBe(1000);
    rerender({ base: 1000, running: true });
    act(() => { vi.advanceTimersByTime(2500); });
    expect(result.current).toBeGreaterThanOrEqual(3000);
    rerender({ base: 5000, running: false });
    expect(result.current).toBe(5000);
  });
});
