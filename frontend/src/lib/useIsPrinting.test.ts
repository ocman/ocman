// @vitest-environment jsdom
//
// Tests for useIsPrinting, the hook that force-expands collapsed tool
// blocks while a session is being printed / saved to PDF. It tracks
// print state via the `print` media query plus the beforeprint /
// afterprint window events.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useIsPrinting } from './useIsPrinting';

interface FakeMql {
  matches: boolean;
  listeners: Set<(e: MediaQueryListEvent) => void>;
  addEventListener: (type: string, cb: (e: MediaQueryListEvent) => void) => void;
  removeEventListener: (type: string, cb: (e: MediaQueryListEvent) => void) => void;
  // Test helper: flip the query and notify subscribers.
  emit: (matches: boolean) => void;
}

function installMatchMedia(initialMatches: boolean): FakeMql {
  const mql: FakeMql = {
    matches: initialMatches,
    listeners: new Set(),
    addEventListener: (_type, cb) => {
      mql.listeners.add(cb);
    },
    removeEventListener: (_type, cb) => {
      mql.listeners.delete(cb);
    },
    emit: (matches) => {
      mql.matches = matches;
      mql.listeners.forEach((cb) => cb({ matches } as MediaQueryListEvent));
    },
  };
  window.matchMedia = vi.fn(() => mql as unknown as MediaQueryList);
  return mql;
}

describe('useIsPrinting', () => {
  const originalMatchMedia = window.matchMedia;

  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    window.matchMedia = originalMatchMedia;
  });

  it('starts false when the print media query is not matching', () => {
    installMatchMedia(false);
    const { result } = renderHook(() => useIsPrinting());
    expect(result.current).toBe(false);
  });

  it('starts true when the print media query already matches (DevTools emulation)', () => {
    installMatchMedia(true);
    const { result } = renderHook(() => useIsPrinting());
    expect(result.current).toBe(true);
  });

  it('flips to true on beforeprint and back to false on afterprint', () => {
    installMatchMedia(false);
    const { result } = renderHook(() => useIsPrinting());
    expect(result.current).toBe(false);

    act(() => {
      window.dispatchEvent(new Event('beforeprint'));
    });
    expect(result.current).toBe(true);

    act(() => {
      window.dispatchEvent(new Event('afterprint'));
    });
    expect(result.current).toBe(false);
  });

  it('tracks media query change events', () => {
    const mql = installMatchMedia(false);
    const { result } = renderHook(() => useIsPrinting());
    expect(result.current).toBe(false);

    act(() => {
      mql.emit(true);
    });
    expect(result.current).toBe(true);

    act(() => {
      mql.emit(false);
    });
    expect(result.current).toBe(false);
  });

  it('removes its listeners on unmount', () => {
    const mql = installMatchMedia(false);
    const removeWin = vi.spyOn(window, 'removeEventListener');
    const { unmount } = renderHook(() => useIsPrinting());

    expect(mql.listeners.size).toBe(1);
    unmount();

    expect(mql.listeners.size).toBe(0);
    expect(removeWin).toHaveBeenCalledWith('beforeprint', expect.any(Function));
    expect(removeWin).toHaveBeenCalledWith('afterprint', expect.any(Function));
  });

  it('does not throw when matchMedia is unavailable', () => {
    // Some environments (older Safari, SSR) lack matchMedia.
    (window as unknown as { matchMedia?: unknown }).matchMedia = undefined;
    const { result } = renderHook(() => useIsPrinting());
    expect(result.current).toBe(false);

    act(() => {
      window.dispatchEvent(new Event('beforeprint'));
    });
    expect(result.current).toBe(true);
  });
});
