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
type ChangedListener = (id: string, session?: unknown, patch?: { title?: string }) => void;
const changedListeners = new Set<ChangedListener>();
vi.mock('../../lib/useGlobalEvents', () => ({
  onSessionChanged: (cb: ChangedListener) => {
    changedListeners.add(cb);
    return () => changedListeners.delete(cb);
  },
}));

import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { HeaderContext, type HeaderInfo } from '../../lib/headerContext';
import type { SessionMetadata } from '../../lib/sessionReducer';
import type { Session } from '../../lib/api';
import { useSessionSeen } from './useSessionSeen';

const ownerQualifiedPatch = useApiStore.getState().patchRecentSession;

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
    useApiStore.setState({ markSessionSeen, patchRecentSession, recentSessions: [] });
    useUiStore.setState({ lastOpenedSessionId: undefined });
  });

  afterEach(() => vi.useRealTimers());

  it('acknowledges only the viewed owner when session IDs match', () => {
    const local = { ...session, platform: 'opencode', status: 'interrupted', seen: false, seenTimeUpdated: 0 } as Session;
    const remote = { ...local, platform: 'r-box:opencode', status: 'busy' as const };
    useApiStore.setState({ recentSessions: [local, remote], patchRecentSession: ownerQualifiedPatch });
    const patchSession = vi.fn();
    const { rerender } = renderHook(
      ({ value }) => useSessionSeen({ session: value, patchSession }),
      { wrapper, initialProps: { value: remote as SessionMetadata } },
    );
    rerender({ value: { ...remote, status: 'interrupted' } });
    expect(useApiStore.getState().recentSessions.find(s => s.platform === local.platform)?.seen).toBe(false);
    expect(useApiStore.getState().recentSessions.find(s => s.platform === remote.platform)?.seen).toBe(true);
    expect(markSessionSeen).toHaveBeenLastCalledWith(remote.platform, remote.id, remote.timeUpdated, true);
  });

  it('acknowledges an interruption while visible even when its timestamp is unchanged', () => {
    const patchSession = vi.fn();
    const { rerender } = renderHook(
      ({ value }) => useSessionSeen({ session: value, patchSession }),
      { wrapper, initialProps: { value: { ...session, status: 'busy' } as SessionMetadata } },
    );
    rerender({ value: { ...session, status: 'interrupted', archived: true } });
    expect(markSessionSeen).toHaveBeenCalledTimes(2);
    expect(markSessionSeen).toHaveBeenLastCalledWith('opencode', 's1', 42, true);
    expect(patchSession).toHaveBeenLastCalledWith({ seen: true });
    expect(patchRecentSession).toHaveBeenLastCalledWith('s1', { seen: true, seenTimeUpdated: 42 }, 'opencode');
  });

  it('does not acknowledge content while hidden, and marks the latest content when visible', async () => {
    vi.useFakeTimers();
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    const patchSession = vi.fn();
    const { rerender, unmount } = renderHook(
      ({ value }) => useSessionSeen({ session: value, patchSession }),
      { wrapper, initialProps: { value: session } },
    );
    rerender({ value: { ...session, timeUpdated: 200 } });
    await act(async () => vi.advanceTimersByTime(500));
    expect(markSessionSeen).not.toHaveBeenCalled();
    expect(patchRecentSession).not.toHaveBeenCalled();
    expect(patchSession).not.toHaveBeenCalled();
    hidden.mockReturnValue(false);
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    expect(markSessionSeen).toHaveBeenLastCalledWith('opencode', 's1', 200);
    unmount();
    hidden.mockRestore();
  });

  it('does not flush a pending read after the tab becomes hidden', () => {
    vi.useFakeTimers();
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
    const patchSession = vi.fn();
    const { rerender, unmount } = renderHook(
      ({ value }) => useSessionSeen({ session: value, patchSession }),
      { wrapper, initialProps: { value: session } },
    );
    rerender({ value: { ...session, timeUpdated: 200 } });
    hidden.mockReturnValue(true);
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    unmount();
    expect(markSessionSeen).toHaveBeenCalledTimes(1);
    expect(markSessionSeen).toHaveBeenCalledWith('opencode', 's1', 42);
    hidden.mockRestore();
  });

  it('does not clear an archive again when an existing tab becomes visible or gets updates', async () => {
    vi.useFakeTimers();
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
    const patchSession = vi.fn();
    const { rerender, unmount } = renderHook(
      ({ value }) => useSessionSeen({ session: value, patchSession }),
      { wrapper, initialProps: { value: session } },
    );
    patchSession.mockClear();
    patchRecentSession.mockClear();
    hidden.mockReturnValue(true);
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    rerender({ value: { ...session, archived: true } });
    hidden.mockReturnValue(false);
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    rerender({ value: { ...session, archived: true, timeUpdated: 200 } });
    await act(async () => vi.advanceTimersByTimeAsync(500));
    expect(patchSession.mock.calls.some(([patch]) => patch.archived === false)).toBe(false);
    expect(patchRecentSession.mock.calls.some(([, patch]) => patch.archived === false)).toBe(false);
    unmount();
    hidden.mockRestore();
  });

  it('marks seen everywhere, records the open, and publishes header info', async () => {
    const patchSession = vi.fn();
    const { unmount } = renderHook(() => useSessionSeen({ session, patchSession }), { wrapper });

    expect(patchSession).toHaveBeenCalledWith({ seen: true, archived: false });
    expect(patchRecentSession).toHaveBeenCalledWith('s1', { seen: true, seenTimeUpdated: 42, archived: false }, 'opencode');
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

  it('labels a worktree session with its project, not the worktree', () => {
    const wt = { ...session, directory: '/src/.worktrees/repo/feat' };
    renderHook(() => useSessionSeen({ session: wt, patchSession: vi.fn() }), { wrapper });
    expect(setInfo).toHaveBeenCalledWith(expect.objectContaining<HeaderInfo>({
      sessionProject: 'src/repo', sessionProjectFull: '/src/.worktrees/repo/feat',
    }));
  });

  it('applies an upstream rename of the open session only', () => {
    const patchSession = vi.fn();
    const { unmount } = renderHook(() => useSessionSeen({ session, patchSession }), { wrapper });
    patchSession.mockClear();
    for (const cb of changedListeners) {
      cb('other', undefined, { title: 'Nope' });
      cb('s1', undefined, {});
      cb('s1', undefined, { title: 'Renamed' });
    }
    expect(patchSession.mock.calls).toEqual([[{ title: 'Renamed' }]]);
    unmount();
    expect(changedListeners.size).toBe(0);
  });

  it('re-reads the title when an identity-only change may have superseded a rename', async () => {
    vi.useFakeTimers();
    const peekSession = vi.fn(async () => ({ session: { ...session, title: 'Renamed' } }));
    useApiStore.setState({ peekSession } as never);
    const patchSession = vi.fn();
    const { unmount } = renderHook(() => useSessionSeen({ session, patchSession }), { wrapper });
    patchSession.mockClear();
    for (const cb of changedListeners) {
      cb('s1', undefined, { status: 'busy' } as never); // status-only: no fetch
      cb('s1');
      cb('s1'); // coalesced into one fetch
    }
    await act(async () => vi.advanceTimersByTime(250));
    expect(peekSession).toHaveBeenCalledTimes(1);
    expect(peekSession).toHaveBeenCalledWith('s1', expect.any(AbortSignal));
    expect(patchSession).toHaveBeenCalledWith({ title: 'Renamed' });
    unmount();
  });

  it('drops a title fetch that a newer rename or fetch overtook', async () => {
    vi.useFakeTimers();
    const resolvers: Array<(title: string) => void> = [];
    const peekSession = vi.fn(() => new Promise((resolve) => {
      resolvers.push((title) => resolve({ session: { ...session, title } }));
    }));
    useApiStore.setState({ peekSession } as never);
    const patchSession = vi.fn();
    const { unmount } = renderHook(() => useSessionSeen({ session, patchSession }), { wrapper });
    const emit = (patch?: { title?: string }) => { for (const cb of changedListeners) cb('s1', undefined, patch); };
    patchSession.mockClear();

    emit();
    await act(async () => vi.advanceTimersByTime(250));
    emit({ title: 'Newest' }); // arrives while the fetch is in flight
    await act(async () => resolvers[0]('Stale'));
    expect(patchSession.mock.calls).toEqual([[{ title: 'Newest' }]]);

    emit();
    await act(async () => vi.advanceTimersByTime(250));
    emit();
    await act(async () => vi.advanceTimersByTime(250));
    await act(async () => resolvers[2]('Second fetch'));
    await act(async () => resolvers[1]('First fetch')); // resolves out of order
    expect(patchSession).toHaveBeenLastCalledWith({ title: 'Second fetch' });
    expect(patchSession).toHaveBeenCalledTimes(2);
    unmount();
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
    expect(patchRecentSession).toHaveBeenLastCalledWith('s1', { seen: true, seenTimeUpdated: 200 }, 'opencode');
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
