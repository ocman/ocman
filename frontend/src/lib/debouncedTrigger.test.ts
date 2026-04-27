import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DebouncedTrigger } from './debouncedTrigger';

describe('DebouncedTrigger', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('does not fire without bump', () => {
    const cb = vi.fn();
    new DebouncedTrigger(cb, { debounceMs: 500, maxWaitMs: 5000 });
    vi.advanceTimersByTime(10000);
    expect(cb).not.toHaveBeenCalled();
  });

  it('fires once after debounceMs of quiet following a single bump', () => {
    const cb = vi.fn();
    const t = new DebouncedTrigger(cb, { debounceMs: 500, maxWaitMs: 5000 });
    t.bump();
    vi.advanceTimersByTime(499);
    expect(cb).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it('resets the inner debounce on each bump', () => {
    const cb = vi.fn();
    const t = new DebouncedTrigger(cb, { debounceMs: 500, maxWaitMs: 5000 });
    t.bump();
    vi.advanceTimersByTime(400);
    t.bump();
    vi.advanceTimersByTime(400);
    // Without the reset behaviour we'd have fired at 500 ms.
    expect(cb).not.toHaveBeenCalled();
    vi.advanceTimersByTime(100);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it('honours maxWaitMs even when bumps keep arriving inside debounce window', () => {
    const cb = vi.fn();
    const t = new DebouncedTrigger(cb, { debounceMs: 500, maxWaitMs: 5000 });
    // Bump every 100 ms for 6 seconds. The inner timer never gets to
    // fire because each bump resets it; the outer maxWait timer must
    // force a fire at the 5 s mark.
    for (let elapsed = 0; elapsed < 5500; elapsed += 100) {
      t.bump();
      vi.advanceTimersByTime(100);
    }
    // Should have fired at the 5000 ms mark inside the loop — exactly
    // once.
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it('starts a fresh burst after firing', () => {
    const cb = vi.fn();
    const t = new DebouncedTrigger(cb, { debounceMs: 500, maxWaitMs: 5000 });
    t.bump();
    vi.advanceTimersByTime(500);
    expect(cb).toHaveBeenCalledTimes(1);
    // New burst — the maxWait timer was cleared by the first fire,
    // so a second bump starts a brand-new pair of timers.
    t.bump();
    vi.advanceTimersByTime(500);
    expect(cb).toHaveBeenCalledTimes(2);
  });

  it('flushNow fires immediately and skips both timers', () => {
    const cb = vi.fn();
    const t = new DebouncedTrigger(cb, { debounceMs: 500, maxWaitMs: 5000 });
    t.bump();
    vi.advanceTimersByTime(100);
    t.flushNow();
    expect(cb).toHaveBeenCalledTimes(1);
    // The previously-scheduled timers must be cancelled so we don't
    // get a second fire later.
    vi.advanceTimersByTime(10000);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it('cancel prevents pending and future fires', () => {
    const cb = vi.fn();
    const t = new DebouncedTrigger(cb, { debounceMs: 500, maxWaitMs: 5000 });
    t.bump();
    t.cancel();
    vi.advanceTimersByTime(10000);
    expect(cb).not.toHaveBeenCalled();
    // Subsequent bumps after cancel should be no-ops too.
    t.bump();
    vi.advanceTimersByTime(10000);
    expect(cb).not.toHaveBeenCalled();
  });

  it('reset re-enables the trigger after cancel', () => {
    const cb = vi.fn();
    const t = new DebouncedTrigger(cb, { debounceMs: 500, maxWaitMs: 5000 });
    t.cancel();
    t.reset();
    t.bump();
    vi.advanceTimersByTime(500);
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it('uses default debounceMs=500 and maxWaitMs=5000 when options omitted', () => {
    const cb = vi.fn();
    const t = new DebouncedTrigger(cb);
    t.bump();
    vi.advanceTimersByTime(499);
    expect(cb).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(cb).toHaveBeenCalledTimes(1);
  });
});
