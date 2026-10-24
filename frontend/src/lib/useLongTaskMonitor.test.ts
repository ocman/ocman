// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

type Entries = { getEntries: () => { duration: number }[] };

// The observer starts when the module loads, so each test stubs
// PerformanceObserver and imports a fresh copy of the module.
async function load(supportedEntryTypes: string[], observe = vi.fn()) {
  let emit: (durations: number[]) => void = () => {};
  class FakePO {
    static supportedEntryTypes = supportedEntryTypes;
    constructor(cb: (list: Entries) => void) {
      emit = (durations) => cb({ getEntries: () => durations.map((duration) => ({ duration })) });
    }
    observe = observe;
  }
  vi.stubGlobal('PerformanceObserver', FakePO);
  vi.resetModules();
  const { useLongTaskMonitor } = await import('./useLongTaskMonitor');
  return { useLongTaskMonitor, emit: (durations: number[]) => emit(durations) };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('useLongTaskMonitor', () => {
  it('counts long tasks and keeps the worst duration', async () => {
    const observe = vi.fn();
    const { useLongTaskMonitor, emit } = await load(['longtask'], observe);
    expect(observe).toHaveBeenCalledWith({ entryTypes: ['longtask'] });

    const { result } = renderHook(() => useLongTaskMonitor());
    expect(result.current).toEqual({ count: 0, maxMs: 0 });

    act(() => emit([80, 200]));
    act(() => emit([60]));
    expect(result.current).toEqual({ count: 3, maxMs: 200 });
  });

  it('ignores synthetic zero-duration entries', async () => {
    const { useLongTaskMonitor, emit } = await load(['longtask']);
    const { result } = renderHook(() => useLongTaskMonitor());
    act(() => emit([0, -5, Number.NaN]));
    expect(result.current).toEqual({ count: 0, maxMs: 0 });
  });

  it('stays at zero when longtask is unsupported', async () => {
    const observe = vi.fn();
    const { useLongTaskMonitor } = await load([], observe);
    expect(observe).not.toHaveBeenCalled();
    expect(renderHook(() => useLongTaskMonitor()).result.current).toEqual({ count: 0, maxMs: 0 });
  });

  it('survives observe() throwing', async () => {
    const { useLongTaskMonitor } = await load(['longtask'], vi.fn(() => { throw new Error('disabled'); }));
    expect(renderHook(() => useLongTaskMonitor()).result.current).toEqual({ count: 0, maxMs: 0 });
  });
});
