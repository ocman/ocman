// @vitest-environment jsdom

import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// jsdom's localStorage lacks working methods in this setup; plant a
// minimal in-memory stub before uiStore's persist middleware loads.
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

import type { Session, SessionDetail } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { computeSidebarHash, visibleSidebarSessions } from '../../lib/sidebarHelpers';

let sessionChanged: ((sessionId: string, session?: Session, patch?: Partial<Session>, platform?: string) => void) | undefined;
let sseConnect: (() => void) | undefined;
let sessionActivity: ((sessionId: string, timeUpdated: number) => void) | undefined;

vi.mock('../../lib/useGlobalEvents', () => ({
  onSessionActivity: (cb: typeof sessionActivity) => {
    sessionActivity = cb;
    return () => { sessionActivity = undefined; };
  },
  onSessionChanged: (cb: typeof sessionChanged) => {
    sessionChanged = cb;
    return () => { sessionChanged = undefined; };
  },
  onSseConnect: (cb: () => void) => {
    sseConnect = cb;
    return () => { sseConnect = undefined; };
  },
}));

import { useSidebarSessions } from './useSidebarSessions';

describe('useSidebarSessions project visibility', () => {
  it('keeps pinned archived sessions and completed children while excluding unpinned rows', async () => {
    const fixtures: Partial<Session>[] = [
      { id: 'open', archived: true },
      { id: 'pin-archived', pinned: true, archived: true },
      { id: 'pin-child', pinned: true, parentId: 'parent', status: 'done' as const },
      { id: 'hidden-archived', archived: true },
      { id: 'hidden-child', parentId: 'parent', status: 'done' as const },
    ];
    const rows = fixtures.map((row) => ({ platform: 'opencode', directory: '/repo', timeUpdated: 1,
      seen: false, seenTimeUpdated: 0, unreadCount: 0, ...row } as Session));
    useApiStore.setState({ getSessions: vi.fn().mockResolvedValue(rows), recentSessions: [], recentSessionsHash: '' });
    const { result } = renderHook(() => useSidebarSessions({
      id: 'open', sessionId: 'open', collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));
    await act(async () => { await result.current.loadRecentSessions(); });
    expect(result.current.recentSessions.map((row) => row.id)).toEqual(['open', 'pin-archived', 'pin-child']);
  });

  it('keeps a quiet project after newer sessions, including when switching views', async () => {
    const sessions = Array.from({ length: 25 }, (_, i) => ({
      id: `session-${i}`, platform: 'opencode', directory: '/repo/busy',
      title: `Work ${i}`, status: 'waiting', timeCreated: Date.now() - i * 1000, timeUpdated: Date.now() - i * 1000,
      seen: false, seenTimeUpdated: 0, unreadCount: 0,
    } as Session));
    const quiet = { ...sessions[0], id: 'dev-stack', directory: '/repo/dev-stack', timeCreated: Date.now() - 60_000, timeUpdated: Date.now() - 60_000 };
    const all = [...sessions, quiet];
    const getSessions = vi.fn(async ({ limit }: { limit?: number } = {}) =>
      all.slice(0, limit === 0 ? undefined : (limit ?? 500)));
    useApiStore.setState({ getSessions, recentSessions: [], recentSessionsHash: '' });
    const { result, rerender } = renderHook(({ sidebarView }: { sidebarView: 'recent' | 'projects' }) =>
      useSidebarSessions({
        id: sessions[0].id, sessionId: sessions[0].id, collapsedProjects: [], sidebarView,
        abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
      }), { initialProps: { sidebarView: 'recent' } });

    await act(async () => { await result.current.loadRecentSessions(); });
    expect(result.current.recentSessions.map((s) => s.id)).toEqual(all.map((s) => s.id));
    await act(async () => { rerender({ sidebarView: 'projects' }); });
    expect(result.current.recentSessions).toContainEqual(quiet);
    expect(result.current.recentSessions).toHaveLength(all.length);
    await act(async () => { rerender({ sidebarView: 'recent' }); });
    expect(result.current.recentSessions.map((s) => s.id)).toEqual(all.map((s) => s.id));
  });

  it('keeps project sessions when adding the open session outside the window', async () => {
    const sessions = Array.from({ length: 25 }, (_, i) => ({
      id: `session-${i}`, platform: 'opencode', directory: '/repo/busy',
      title: `Work ${i}`, status: 'waiting', timeUpdated: Date.now(),
      seen: false, seenTimeUpdated: 0, unreadCount: 0,
    } as Session));
    const open = { ...sessions[0], id: 'older-open', directory: '/repo/older', timeUpdated: Date.now() - 96 * 60 * 60 * 1000 };
    useApiStore.setState({
      getSessions: vi.fn().mockResolvedValue(sessions),
      getSession: vi.fn().mockResolvedValue({ session: open }),
      recentSessions: [], recentSessionsHash: '',
    });
    const { result } = renderHook(() => useSidebarSessions({
      id: open.id, sessionId: open.id, collapsedProjects: [], sidebarView: 'projects',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));
    await act(async () => { await result.current.loadRecentSessions(); });
    expect(result.current.recentSessions).toHaveLength(26);
    expect(result.current.recentSessions).toContainEqual(expect.objectContaining(open));
    expect(result.current.recentSessions).toContainEqual(sessions[24]);
  });

  it('keeps all recent sessions including older pinned sessions', async () => {
    const sessions = Array.from({ length: 25 }, (_, i) => ({
      id: `session-${i}`, platform: 'opencode', directory: '/repo',
      title: `Work ${i}`, status: 'waiting', timeCreated: Date.now() - i * 1000, timeUpdated: Date.now() - i * 1000,
      pinned: i === 24, pinnedAt: i === 24 ? 1 : 0,
      seen: false, seenTimeUpdated: 0, unreadCount: 0,
    } as Session));
    const getSessions = vi.fn(async ({ limit }: { limit?: number } = {}) =>
      sessions.slice(0, limit === 0 ? undefined : (limit ?? 500)));
    useApiStore.setState({ getSessions, recentSessions: [], recentSessionsHash: '' });
    const { result } = renderHook(() => useSidebarSessions({
      id: sessions[0].id, sessionId: sessions[0].id, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));

    await act(async () => { await result.current.loadRecentSessions(); });

    expect(result.current.recentSessions.map((s) => s.id)).toEqual(sessions.map((s) => s.id));
    expect(result.current.recentSessions).toContainEqual(sessions[24]);
  });
});

describe('useSidebarSessions live refresh', () => {
  const getSessions = vi.fn().mockResolvedValue([]);

  beforeEach(() => {
    getSessions.mockReset().mockResolvedValue([]);
    sessionChanged = undefined;
    sseConnect = undefined;
    useApiStore.setState({
      getSessions,
      recentSessions: [{ id: 'session-1', status: 'done' } as Session],
      recentSessionsHash: '',
    });
  });

  it('refreshes unread state immediately after a crash status event', async () => {
    const busy = { id: 'crashed', platform: 'opencode', status: 'busy', seen: true, timeUpdated: 100, seenTimeUpdated: 100 } as Session;
    const interrupted = { ...busy, status: 'interrupted', seen: false } as Session;
    useApiStore.setState({ recentSessions: [busy], peekSession: vi.fn().mockResolvedValue({ session: interrupted }) });
    const abortSignalRef = { current: new AbortController() };
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef, navigate: vi.fn(),
    }));
    await act(async () => sessionChanged?.('crashed', undefined, { status: 'interrupted' }, 'opencode'));
    expect(useApiStore.getState().recentSessions[0]).toMatchObject({ status: 'interrupted', seen: false });
  });

  it('does not undo viewing an interruption while its status refresh is in flight', async () => {
    const busy = { id: 'crashed', platform: 'opencode', status: 'busy', seen: true, timeUpdated: 100, seenTimeUpdated: 100 } as Session;
    const interrupted = { ...busy, status: 'interrupted', seen: false } as Session;
    let finish!: (value: SessionDetail) => void;
    const peekSession = vi.fn(() => new Promise<SessionDetail>((resolve) => { finish = resolve; }));
    useApiStore.setState({ recentSessions: [busy], peekSession });
    const abortSignalRef = { current: new AbortController() };
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef, navigate: vi.fn(),
    }));
    act(() => sessionChanged?.('crashed', undefined, { status: 'interrupted' }, 'opencode'));
    act(() => useApiStore.getState().patchRecentSession('crashed', { seen: true }, 'opencode'));
    await act(async () => finish({ session: interrupted, messages: [], parts: [] }));
    expect(useApiStore.getState().recentSessions[0].seen).toBe(true);
  });

  it('updates background activity without reordering or refetching and ignores older events', () => {
    useApiStore.setState({ recentSessions: [
      { id: 'first', timeCreated: 2, lastTurnCompletedAt: 120_000, timeUpdated: 120_000 },
      { id: 'background', timeCreated: 1, lastTurnCompletedAt: 60_000, timeUpdated: 60_000 },
    ] as Session[] });
    const { unmount } = renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));
    act(() => sessionActivity?.('background', 180_000));
    expect(useApiStore.getState().recentSessions.map((s) => s.id)).toEqual(['first', 'background']);
    act(() => sessionActivity?.('background', 90_000));
    expect(useApiStore.getState().recentSessions[1].timeUpdated).toBe(180_000);
    expect(getSessions).not.toHaveBeenCalled();
    unmount();
    expect(sessionActivity).toBeUndefined();
  });

  it('ignores per-token activity that stays within the same minute bucket', () => {
    useApiStore.setState({ recentSessions: [{ id: 'streaming', timeUpdated: 120_000 }] as Session[] });
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));
    const before = useApiStore.getState().recentSessions;
    act(() => sessionActivity?.('streaming', 150_000));
    expect(useApiStore.getState().recentSessions).toBe(before);
    act(() => sessionActivity?.('streaming', 180_000));
    expect(useApiStore.getState().recentSessions[0].timeUpdated).toBe(180_000);
  });

  it('loads an unknown active session once and applies its latest streaming timestamp', async () => {
    let resolve!: (value: { session: Session }) => void;
    const peekSession = vi.fn(() => new Promise<{ session: Session }>((done) => { resolve = done; }));
    useApiStore.setState({ peekSession: peekSession as never, recentSessions: [] });
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));
    act(() => { sessionActivity?.('old', 180_000); sessionActivity?.('old', 180_001); });
    expect(peekSession).toHaveBeenCalledOnce();
    await act(async () => { resolve({ session: { id: 'old', timeUpdated: 1, directory: '/repo', status: 'busy' } as Session }); });
    expect(useApiStore.getState().recentSessions[0]).toMatchObject({ id: 'old', timeUpdated: 180_001 });
    expect(getSessions).not.toHaveBeenCalled();
  });

  it('keeps an old archived pinned child discovered through activity', async () => {
    const row = { id: 'pin', pinned: true, archived: true, parentId: 'parent',
      timeUpdated: 1, directory: '/repo', status: 'done' } as Session;
    useApiStore.setState({ peekSession: vi.fn().mockResolvedValue({ session: row }), recentSessions: [] });
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));
    await act(async () => { sessionActivity?.('pin', Date.now()); });
    expect(useApiStore.getState().recentSessions).toEqual([row]);
  });

  it('does not resurface an idle old session on replayed activity from a new instance', async () => {
    // A freshly launched instance emits message events for old sessions; the
    // backend stamps them "now". An idle row must keep its real timestamp.
    const peekSession = vi.fn().mockResolvedValue({ session: {
      id: 'stale', timeUpdated: 1, directory: '/repo', status: 'waiting',
    } as Session });
    useApiStore.setState({ peekSession, recentSessions: [], recentSessionsHash: '' });
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));
    await act(async () => { sessionActivity?.('stale', Date.now()); });
    expect(useApiStore.getState().recentSessions).toEqual([]);
  });

  it('keeps a busy archived session hidden on activity', async () => {
    const peekSession = vi.fn().mockResolvedValue({ session: {
      id: 'archived', timeUpdated: Date.now(), directory: '/repo', status: 'busy', archived: true,
    } as Session });
    useApiStore.setState({ peekSession, recentSessions: [], recentSessionsHash: '' });
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));
    await act(async () => { sessionActivity?.('archived', Date.now()); });
    expect(peekSession).toHaveBeenCalledWith('archived', expect.anything());
    expect(useApiStore.getState().recentSessions).toEqual([]);
  });

  it('refreshes on session changes and SSE reconnects', async () => {
    const abortController = new AbortController();
    renderHook(() => useSidebarSessions({
      id: undefined,
      sessionId: 'session-1',
      collapsedProjects: [],
      sidebarView: 'recent',
      abortSignalRef: { current: abortController },
      navigate: vi.fn(),
    }));

    await waitFor(() => expect(getSessions).toHaveBeenCalledTimes(1));

    act(() => sessionChanged?.('session-1', undefined, { status: 'busy' }));
    expect(useApiStore.getState().recentSessions[0].status).toBe('busy');
    expect(getSessions).toHaveBeenCalledTimes(1);

    act(() => sessionChanged?.('session-1'));
    await waitFor(() => expect(getSessions).toHaveBeenCalledTimes(2));

    act(() => sseConnect?.());
    await waitFor(() => expect(getSessions).toHaveBeenCalledTimes(3));
  });

  it.each(['done', 'waiting', 'error'] as const)('refreshes durable completion on a %s status patch', async (status) => {
    const first = { id: 'first', platform: 'opencode', timeCreated: 1, timeUpdated: 120_000, lastTurnCompletedAt: 120_000 } as Session;
    const background = { ...first, id: 'background', status: 'busy' as const, lastTurnCompletedAt: 60_000 };
    // The global list snapshot can still be stale at the terminal edge.
    getSessions.mockResolvedValueOnce([first, background]);
    const peekSession = vi.fn().mockResolvedValue({ session: { ...background, status, lastTurnCompletedAt: 180_000 } });
    useApiStore.setState({ peekSession, recentSessions: [first, background], recentSessionsHash: '' });
    const abortSignalRef = { current: new AbortController() };
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef, navigate: vi.fn(),
    }));
    await act(async () => { sessionChanged?.('background', undefined, { status }); });
    expect(getSessions).not.toHaveBeenCalled();
    expect(peekSession).toHaveBeenCalledWith('background', expect.anything(), background.platform);
    expect(useApiStore.getState().recentSessions.map(s => s.id)).toEqual(['background', 'first']);
    expect(useApiStore.getState().recentSessions[0].lastTurnCompletedAt).toBe(180_000);
    expect(useApiStore.getState().recentSessions[0].status).toBe(status);
  });

  it('qualifies completion fetches and patches when owners share a session ID', async () => {
    const local = { id: 'shared', platform: 'opencode', status: 'busy', timeCreated: 1,
      timeUpdated: 120_000, lastTurnCompletedAt: 120_000 } as Session;
    const remote = { ...local, platform: 'r-owner:opencode', lastTurnCompletedAt: 60_000 };
    getSessions.mockResolvedValueOnce([local, remote]);
    const peekSession = vi.fn().mockResolvedValue({ session: { ...remote, lastTurnCompletedAt: 180_000 } });
    useApiStore.setState({ peekSession, recentSessions: [local, remote], recentSessionsHash: '' });
    const abortSignalRef = { current: new AbortController() };
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent', abortSignalRef, navigate: vi.fn(),
    }));
    await act(async () => { sessionChanged?.('shared', undefined, { status: 'waiting' }, remote.platform); });
    expect(peekSession).toHaveBeenCalledWith('shared', expect.anything(), remote.platform);
    expect(useApiStore.getState().recentSessions.find(s => s.platform === local.platform)).toMatchObject({
      status: 'busy', lastTurnCompletedAt: 120_000,
    });
    expect(useApiStore.getState().recentSessions[0]).toMatchObject({ platform: remote.platform, status: 'waiting', lastTurnCompletedAt: 180_000 });
  });

  it('only refetches ambiguous unqualified events instead of guessing an owner', async () => {
    const rows = ['opencode', 'r-owner:opencode'].map(platform => ({ id: 'shared', platform,
      status: 'busy', timeCreated: 1, timeUpdated: 1 } as Session));
    getSessions.mockResolvedValueOnce(rows);
    const peekSession = vi.fn();
    useApiStore.setState({ peekSession, recentSessions: rows, recentSessionsHash: '' });
    const abortSignalRef = { current: new AbortController() };
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent', abortSignalRef, navigate: vi.fn(),
    }));
    await act(async () => { sessionChanged?.('shared', undefined, { status: 'waiting' }); });
    expect(peekSession).not.toHaveBeenCalled();
    expect(getSessions).toHaveBeenCalledOnce();
    expect(useApiStore.getState().recentSessions.every(s => s.status === 'busy')).toBe(true);
  });

  it.each([{ id: 'other', platform: 'opencode' }, { id: 'shared', platform: 'r-other:opencode' }])(
    'ignores a completion response with mismatched identity %j', async (identity) => {
      const row = { id: 'shared', platform: 'opencode', status: 'busy', timeCreated: 1,
        timeUpdated: 1, lastTurnCompletedAt: 100 } as Session;
      getSessions.mockResolvedValueOnce([row]);
      useApiStore.setState({ recentSessions: [row], recentSessionsHash: '',
        peekSession: vi.fn().mockResolvedValue({ session: { ...row, ...identity, lastTurnCompletedAt: 999 } }) });
      const abortSignalRef = { current: new AbortController() };
      renderHook(() => useSidebarSessions({
        id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent', abortSignalRef, navigate: vi.fn(),
      }));
      await act(async () => { sessionChanged?.(row.id, undefined, { status: 'waiting' }, row.platform); });
      expect(useApiStore.getState().recentSessions[0].lastTurnCompletedAt).toBe(100);
    },
  );

  it('does not let a delayed completion response hide the next running turn', async () => {
    const row = { id: 'session', platform: 'r-owner:opencode', status: 'busy', timeCreated: 1,
      timeUpdated: 1, lastTurnCompletedAt: 100 } as Session;
    let finish!: (result: { session: Session }) => void;
    const peekSession = vi.fn(() => new Promise<{ session: Session }>(resolve => { finish = resolve; }));
    getSessions.mockResolvedValueOnce([{ ...row, status: 'waiting' }]);
    useApiStore.setState({ recentSessions: [row], recentSessionsHash: '', peekSession: peekSession as never });
    const abortSignalRef = { current: new AbortController() };
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent', abortSignalRef, navigate: vi.fn(),
    }));
    await act(async () => { sessionChanged?.(row.id, undefined, { status: 'waiting' }, row.platform); });
    act(() => { sessionChanged?.(row.id, undefined, { status: 'busy' }, row.platform); });
    await act(async () => { finish({ session: { ...row, status: 'waiting', lastTurnCompletedAt: 200 } }); });
    expect(useApiStore.getState().recentSessions[0]).toMatchObject({ status: 'busy', lastTurnCompletedAt: 200 });
    expect(peekSession).toHaveBeenCalledOnce();
    expect(getSessions).not.toHaveBeenCalled();
  });

  it.each(['(auto-approve subagent)', 'Research (@explore subagent)'])(
    'keeps hidden internal session %s out when SSE announces activity', async (title) => {
      const peekSession = vi.fn().mockResolvedValue({ session: {
        id: 'internal', title, parentId: '', directory: '/repo', status: 'busy', timeUpdated: 1,
      } as Session });
      useApiStore.setState({ peekSession, recentSessions: [], recentSessionsHash: '' });
      renderHook(() => useSidebarSessions({
        id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
        abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
      }));
      await act(async () => { sessionActivity?.('internal', 180_000); });
      expect(useApiStore.getState().recentSessions).toEqual([]);
      await act(async () => { sessionActivity?.('internal', 180_001); });
      expect(peekSession).toHaveBeenCalledOnce();
    },
  );
});

describe('useSidebarSessions project collapse', () => {
  const DIR = '/repo/aspect-infra';
  const open = { id: 'session-1', status: 'done', directory: DIR } as Session;
  const getSessions = vi.fn().mockResolvedValue([open]);

  // Mirrors how SessionDetail wires it: uiStore selector -> prop.
  const mount = () => renderHook(() => {
    const collapsedProjects = useUiStore((s) => s.collapsedProjects);
    return useSidebarSessions({
      id: 'session-1',
      sessionId: 'session-1',
      collapsedProjects,
      sidebarView: 'projects',
      abortSignalRef: { current: new AbortController() },
      navigate: vi.fn(),
    });
  });

  beforeEach(() => {
    getSessions.mockClear();
    useApiStore.setState({ getSessions, recentSessions: [open], recentSessionsHash: '' });
    useUiStore.setState({ collapsedProjects: [DIR] });
  });

  it('persists the expansion so the group does not re-collapse on navigation', async () => {
    const { result } = mount();
    await waitFor(() =>
      expect(useUiStore.getState().collapsedProjects).not.toContain(DIR),
    );
    expect(result.current.collapsedProjectSet.has(DIR)).toBe(false);
  });

  it('keeps a user-initiated collapse of the open project collapsed', async () => {
    mount();
    await waitFor(() =>
      expect(useUiStore.getState().collapsedProjects).not.toContain(DIR),
    );

    // The user deliberately collapses the project they are working in.
    act(() => { useUiStore.setState({ collapsedProjects: [DIR] }); });

    // A later sidebar update (SSE status patch, reconciliation) must not
    // silently undo that choice.
    await act(async () => {
      useApiStore.getState().patchRecentSession('session-1', { status: 'busy' });
    });
    expect(useUiStore.getState().collapsedProjects).toContain(DIR);
  });
});

describe('useSidebarSessions archive navigation', () => {
  it('retains a pinned session after archiving and reloading with archived hidden', async () => {
    vi.useFakeTimers();
    const pinned = { id: 'pin', platform: 'opencode', pinned: true, archived: false,
      timeUpdated: 1, seen: false, seenTimeUpdated: 0, unreadCount: 0 } as Session;
    const archived = { ...pinned, archived: true };
    useApiStore.setState({ recentSessions: [pinned], recentSessionsHash: computeSidebarHash([pinned]),
      getSessions: vi.fn().mockResolvedValue([archived]), archiveSession: vi.fn().mockResolvedValue(undefined) });
    try {
      const { result } = renderHook(() => useSidebarSessions({
        id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
        abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
      }));
      act(() => result.current.handleArchiveSession({ stopPropagation: vi.fn() } as never, pinned));
      await act(async () => { await vi.advanceTimersByTimeAsync(300); });
      expect(result.current.recentSessions).toEqual([archived]);
      await act(async () => { await result.current.loadRecentSessions(); });
      expect(result.current.recentSessions).toEqual([archived]);
    } finally {
      vi.useRealTimers();
    }
  });

  it('opens the next row the filtered sidebar shows, not a hidden session', async () => {
    vi.useFakeTimers();
    const sessions = ['cur', 'hidden', 'next'].map((id) => ({ id, platform: 'opencode', timeUpdated: 1 } as Session));
    useApiStore.setState({ recentSessions: sessions, recentSessionsHash: '', archiveSession: vi.fn().mockResolvedValue(undefined) });
    visibleSidebarSessions.current = [sessions[0], sessions[2]];
    const navigate = vi.fn();
    try {
      const { result } = renderHook(() => useSidebarSessions({
        id: 'cur', sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
        abortSignalRef: { current: new AbortController() }, navigate,
      }));
      act(() => result.current.handleArchiveSession({ stopPropagation: vi.fn() } as never, sessions[0]));
      await act(async () => { await vi.advanceTimersByTimeAsync(300); });
      expect(navigate).toHaveBeenCalledWith('/session/next');
    } finally {
      visibleSidebarSessions.current = null;
      vi.useRealTimers();
    }
  });
});
