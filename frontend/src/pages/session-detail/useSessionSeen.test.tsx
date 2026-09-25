// @vitest-environment jsdom

import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';

vi.hoisted(() => {
  const mem = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => mem.get(key) ?? null,
      setItem: (key: string, value: string) => void mem.set(key, value),
      removeItem: (key: string) => void mem.delete(key),
    },
  });
});

const recheckFaviconNotify = vi.fn();
vi.mock('../../lib/useFaviconNotify', () => ({ recheckFaviconNotify: () => recheckFaviconNotify() }));

import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { HeaderContext, type HeaderInfo } from '../../lib/headerContext';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { useSessionSeen } from './useSessionSeen';

const session = {
  id: 's1', platform: 'opencode', directory: '/home/u/repo', title: 'Fix bug', timeUpdated: 42, remoteId: 'r1',
} as SessionMetadata;

describe('useSessionSeen', () => {
  const markSessionSeen = vi.fn(async () => ({ ok: true }));
  const patchRecentSession = vi.fn();
  const setInfo = vi.fn();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <HeaderContext.Provider value={{ info: {}, setInfo }}>{children}</HeaderContext.Provider>
  );

  beforeEach(() => {
    vi.clearAllMocks();
    useApiStore.setState({ markSessionSeen, patchRecentSession });
    useUiStore.setState({ lastOpenedSessionId: undefined });
  });

  afterEach(() => vi.useRealTimers());

  it('marks seen everywhere, records the open, and publishes header info', async () => {
    const patchSession = vi.fn();
    const { unmount } = renderHook(() => useSessionSeen({ session, patchSession }), { wrapper });

    expect(patchSession).toHaveBeenCalledWith({ seen: true, archived: false });
    expect(patchRecentSession).toHaveBeenCalledWith('s1', { seen: true, archived: false });
    expect(markSessionSeen).toHaveBeenCalledWith('opencode', 's1', 42);
    await waitFor(() => expect(recheckFaviconNotify).toHaveBeenCalled());
    expect(useUiStore.getState().lastOpenedSessionId).toBe('s1');
    expect(document.title).toBe('Fix bug - ocman');
    expect(setInfo).toHaveBeenCalledWith(expect.objectContaining<HeaderInfo>({
      sessionId: 's1', sessionTitle: 'Fix bug', sessionRemoteId: 'r1', sessionProjectFull: '/home/u/repo',
    }));

    unmount();
    expect(setInfo).toHaveBeenLastCalledWith({});
  });

  it('does nothing until the session has loaded', () => {
    renderHook(() => useSessionSeen({ session: null, patchSession: vi.fn() }), { wrapper });
    expect(markSessionSeen).not.toHaveBeenCalled();
    expect(setInfo).not.toHaveBeenCalled();
    expect(document.title).toBe('Session - ocman');
  });

  it('marks each identity on entry and flushes its newer watermark when leaving', () => {
    const patchSession = vi.fn();
    const { rerender } = renderHook(
      ({ value }) => useSessionSeen({ session: value, patchSession }),
      { wrapper, initialProps: { value: session } },
    );
    rerender({ value: { ...session, timeUpdated: 100 } });
    expect(markSessionSeen).toHaveBeenCalledTimes(1);
    rerender({ value: { ...session, id: 's2', timeUpdated: 200 } });
    expect(markSessionSeen).toHaveBeenLastCalledWith('opencode', 's2', 200);
    rerender({ value: { ...session, platform: 'r-other:opencode', timeUpdated: 300 } });
    expect(markSessionSeen).toHaveBeenLastCalledWith('r-other:opencode', 's1', 300);
    rerender({ value: session });
    expect(markSessionSeen.mock.calls).toEqual([
      ['opencode', 's1', 42],
      ['opencode', 's1', 100],
      ['opencode', 's2', 200],
      ['r-other:opencode', 's1', 300],
      ['opencode', 's1', 42],
    ]);
  });

  it('advances a cached entry watermark to the authoritative timestamp once the burst settles', async () => {
    vi.useFakeTimers();
    const patchSession = vi.fn();
    const { rerender, unmount } = renderHook(
      ({ value }) => useSessionSeen({ session: value, patchSession }),
      { wrapper, initialProps: { value: session } },
    );
    expect(markSessionSeen).toHaveBeenLastCalledWith('opencode', 's1', 42);
    rerender({ value: { ...session, timeUpdated: 100, seen: false } });
    await act(async () => vi.advanceTimersByTime(400));
    rerender({ value: { ...session, timeUpdated: 200, seen: false } });
    await act(async () => vi.advanceTimersByTime(499));
    expect(markSessionSeen).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTime(1));
    expect(markSessionSeen).toHaveBeenCalledTimes(2);
    expect(markSessionSeen).toHaveBeenLastCalledWith('opencode', 's1', 200);
    expect(patchRecentSession).toHaveBeenLastCalledWith('s1', { seen: true, archived: false });
    expect(recheckFaviconNotify).toHaveBeenCalledTimes(2);
    rerender({ value: { ...session, timeUpdated: 200 } });
    await act(async () => vi.advanceTimersByTime(1000));
    expect(markSessionSeen).toHaveBeenCalledTimes(2);
    unmount();
  });

  it('flushes the latest observed watermark on unmount and cancels its timer', async () => {
    vi.useFakeTimers();
    const patchSession = vi.fn();
    const { rerender, unmount } = renderHook(
      ({ value }) => useSessionSeen({ session: value, patchSession }),
      { wrapper, initialProps: { value: session } },
    );
    rerender({ value: { ...session, timeUpdated: 200 } });
    unmount();
    expect(markSessionSeen).toHaveBeenLastCalledWith('opencode', 's1', 200);
    await act(async () => vi.advanceTimersByTime(500));
    expect(markSessionSeen).toHaveBeenCalledTimes(2);
    expect(patchSession).toHaveBeenCalledTimes(1);
  });
});
