// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The monitor reads ?debug from window.location.search at first call
// and caches the result. Reset the module between tests so each one
// starts from a clean state with a fresh enabled-flag read.
beforeEach(async () => {
  vi.resetModules();
  // Default to ?debug enabled — most tests want to exercise the
  // counting logic. The "no-op when ?debug is absent" test resets
  // the modules itself with a clean URL.
  window.history.replaceState({}, '', '/?debug');
});

afterEach(() => {
  vi.useRealTimers();
  window.history.replaceState({}, '', '/');
});

describe('trackRender', () => {
  it('is a no-op when ?debug is not present in the URL', async () => {
    window.history.replaceState({}, '', '/');
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { trackRender } = await import('./renderRateMonitor');

    for (let i = 0; i < 1000; i++) trackRender('hot');
    expect(warn).not.toHaveBeenCalled();
    warn.mockRestore();
  });

  it('warns when the per-second budget is exceeded', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { trackRender } = await import('./renderRateMonitor');
    // Default budget is 80 renders/sec. 200 in tight succession
    // should certainly trip it.
    for (let i = 0; i < 200; i++) trackRender('hot', { i });
    expect(warn).toHaveBeenCalled();
    const message = warn.mock.calls[0][0] as string;
    expect(message).toMatch(/"hot" rendered/);
    warn.mockRestore();
  });

  it('rate-limits warnings to once per cooldown window', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { trackRender } = await import('./renderRateMonitor');
    for (let i = 0; i < 200; i++) trackRender('hot');
    // First batch trips the warning; the cooldown suppresses any
    // duplicates within the next 2 seconds.
    const initial = warn.mock.calls.length;
    expect(initial).toBeGreaterThan(0);
    for (let i = 0; i < 200; i++) trackRender('hot');
    expect(warn.mock.calls.length).toBe(initial);
    warn.mockRestore();
  });

  it('resets the count when a new one-second window starts', async () => {
    vi.useFakeTimers();
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { trackRender, default: _ } = await import('./renderRateMonitor') as
      typeof import('./renderRateMonitor') & { default?: unknown };
    void _;
    // Renders just under the budget shouldn't warn.
    for (let i = 0; i < 25; i++) trackRender('cold');
    // Advance past the window boundary; the next render should reset
    // the count back to 1 instead of accumulating.
    vi.advanceTimersByTime(2000);
    for (let i = 0; i < 25; i++) trackRender('cold');
    expect(warn).not.toHaveBeenCalled();
    warn.mockRestore();
  });

  it('exposes a devtools handle with snapshot/reset/setBudget', async () => {
    const { trackRender } = await import('./renderRateMonitor');
    trackRender('foo', { v: 1 });
    trackRender('bar', { v: 2 });

    interface RenderRatesHandle {
      snapshot(): Record<string, { count: number; totalCount: number; lastProps: unknown }>;
      reset(): void;
      setBudget(n: number): void;
      enable(): void;
    }
    const handle = (window as unknown as { __ocmanRenderRates: RenderRatesHandle }).__ocmanRenderRates;
    expect(handle).toBeDefined();

    const snap = handle.snapshot();
    expect(snap.foo.totalCount).toBe(1);
    expect(snap.bar.totalCount).toBe(1);

    handle.reset();
    expect(Object.keys(handle.snapshot())).toHaveLength(0);
  });
});

describe('logChange', () => {
  it('logs a value the first time it is seen and stays silent on repeats', async () => {
    const info = vi.spyOn(console, 'info').mockImplementation(() => {});
    const { logChange } = await import('./renderRateMonitor');

    logChange('id', 'aaa');
    logChange('id', 'aaa');
    logChange('id', 'aaa');
    expect(info).toHaveBeenCalledTimes(1);

    logChange('id', 'bbb');
    expect(info).toHaveBeenCalledTimes(2);

    info.mockRestore();
  });

  it('is a no-op when ?debug is not present', async () => {
    window.history.replaceState({}, '', '/');
    const info = vi.spyOn(console, 'info').mockImplementation(() => {});
    const { logChange } = await import('./renderRateMonitor');
    logChange('id', 'aaa');
    expect(info).not.toHaveBeenCalled();
    info.mockRestore();
  });
});
