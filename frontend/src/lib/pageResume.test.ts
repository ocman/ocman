// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { onPageResume, RESUME_AFTER_MS } from './pageResume';

let hidden = false;
Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });

function setHidden(next: boolean) {
  hidden = next;
  document.dispatchEvent(new Event('visibilitychange'));
}

describe('onPageResume', () => {
  let resumed: ReturnType<typeof vi.fn<() => void>>;
  let unsubscribe: () => void;
  beforeEach(() => {
    vi.useFakeTimers();
    hidden = false;
    resumed = vi.fn<() => void>();
    unsubscribe = onPageResume(resumed);
  });
  afterEach(() => {
    unsubscribe();
    vi.useRealTimers();
  });

  it('fires on return only after a long enough absence', () => {
    setHidden(true);
    vi.advanceTimersByTime(RESUME_AFTER_MS - 1);
    setHidden(false);
    expect(resumed).not.toHaveBeenCalled();

    setHidden(true);
    vi.advanceTimersByTime(RESUME_AFTER_MS);
    setHidden(false);
    expect(resumed).toHaveBeenCalledOnce();
  });

  it('fires when the browser comes back online', () => {
    window.dispatchEvent(new Event('online'));
    expect(resumed).toHaveBeenCalledOnce();
  });

  it('fires when the clock jumps while visible (machine slept)', () => {
    vi.advanceTimersByTime(10_000);
    expect(resumed).not.toHaveBeenCalled();
    // Sleep: wall clock moves, timers do not.
    vi.setSystemTime(Date.now() + 5 * 60_000);
    vi.advanceTimersByTime(10_000);
    expect(resumed).toHaveBeenCalledOnce();
  });

  it('stops listening once the last subscriber leaves', () => {
    unsubscribe();
    window.dispatchEvent(new Event('online'));
    expect(resumed).not.toHaveBeenCalled();
  });
});
