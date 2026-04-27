import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  _peekForTests,
  _resetForTests,
  _subscribeForTests,
  recordEntry,
  startLongTaskObserver,
} from './useLongTaskMonitor';

// We test the module-level controller; the React hook is a thin
// adapter over it (same convention as usePwaInstall.test.ts).

describe('useLongTaskMonitor controller', () => {
  beforeEach(() => {
    _resetForTests();
  });

  it('starts with zero stats', () => {
    const s = _peekForTests();
    expect(s.count).toBe(0);
    expect(s.maxMs).toBe(0);
    expect(s.lastMs).toBe(0);
  });

  it('records duration and timestamp of an observed entry', () => {
    recordEntry({ duration: 120, startTime: 1000 });
    const s = _peekForTests();
    expect(s.count).toBe(1);
    expect(s.maxMs).toBe(120);
    expect(s.lastMs).toBe(120);
    expect(s.lastAt).toBe(1000);
  });

  it('keeps the maximum across multiple entries', () => {
    recordEntry({ duration: 80, startTime: 100 });
    recordEntry({ duration: 200, startTime: 200 });
    recordEntry({ duration: 60, startTime: 300 });
    const s = _peekForTests();
    expect(s.count).toBe(3);
    expect(s.maxMs).toBe(200);
    // lastMs reflects the most recent task, not the worst.
    expect(s.lastMs).toBe(60);
    expect(s.lastAt).toBe(300);
  });

  it('ignores synthetic zero-duration entries', () => {
    recordEntry({ duration: 0, startTime: 100 });
    recordEntry({ duration: -5, startTime: 200 });
    expect(_peekForTests().count).toBe(0);
  });

  it('notifies subscribers on each recorded entry', () => {
    const updates: number[] = [];
    _subscribeForTests((s) => updates.push(s.count));
    recordEntry({ duration: 100, startTime: 1 });
    recordEntry({ duration: 200, startTime: 2 });
    expect(updates).toEqual([1, 2]);
  });

  it('returns a no-op disposer when PerformanceObserver is undefined', () => {
    const orig = (globalThis as unknown as { PerformanceObserver?: unknown }).PerformanceObserver;
    (globalThis as unknown as { PerformanceObserver?: unknown }).PerformanceObserver = undefined;
    try {
      const stop = startLongTaskObserver();
      expect(typeof stop).toBe('function');
      // Should not throw.
      stop();
    } finally {
      (globalThis as unknown as { PerformanceObserver?: unknown }).PerformanceObserver = orig;
    }
  });

  it('returns a no-op disposer when longtask is not in supportedEntryTypes (e.g. Safari)', () => {
    const orig = globalThis.PerformanceObserver;
    // Stand up a fake PO with empty supportedEntryTypes.
    class FakePO {
      static supportedEntryTypes: string[] = [];
      observe = vi.fn();
      disconnect = vi.fn();
    }
    (globalThis as unknown as { PerformanceObserver: unknown }).PerformanceObserver = FakePO;
    try {
      const stop = startLongTaskObserver();
      // observer should not have been created; disposer is a no-op.
      stop();
    } finally {
      (globalThis as unknown as { PerformanceObserver: unknown }).PerformanceObserver = orig;
    }
  });

  it('starts an observer when longtask is supported', () => {
    const orig = globalThis.PerformanceObserver;
    const observe = vi.fn();
    const disconnect = vi.fn();
    class FakePO {
      static supportedEntryTypes: string[] = ['longtask'];
      observe = observe;
      disconnect = disconnect;
    }
    (globalThis as unknown as { PerformanceObserver: unknown }).PerformanceObserver = FakePO;
    try {
      const stop = startLongTaskObserver();
      expect(observe).toHaveBeenCalledWith({ entryTypes: ['longtask'] });
      stop();
      expect(disconnect).toHaveBeenCalled();
    } finally {
      (globalThis as unknown as { PerformanceObserver: unknown }).PerformanceObserver = orig;
    }
  });
});
