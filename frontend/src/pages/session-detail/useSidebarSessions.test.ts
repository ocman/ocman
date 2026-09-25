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

import type { Session } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';

let sessionChanged: ((sessionId: string, session?: Session, patch?: Partial<Session>) => void) | undefined;
let sseConnect: (() => void) | undefined;
let sessionActivity: ((sessionId: string, timeUpdated: number) => void) | undefined;

vi.mock('../../lib/useGlobalEvents', () => ({
  onSessionActivity: (cb: typeof sessionActivity) => {
    sessionActivity = cb;
    return () => { sessionActivity = undefined; };
  },
  onSessionChanged: (cb: (sessionId: string, session?: Session, patch?: Partial<Session>) => void) => {
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
  it('keeps a quiet project after newer sessions, including when switching views', async () => {
    const sessions = Array.from({ length: 25 }, (_, i) => ({
      id: `session-${i}`, platform: 'opencode', directory: '/repo/busy',
      title: `Work ${i}`, status: 'waiting', timeUpdated: Date.now() - i * 1000,
    } as Session));
    const quiet = { ...sessions[0], id: 'dev-stack', directory: '/repo/dev-stack', timeUpdated: Date.now() - 60_000 };
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
      title: `Work ${i}`, status: 'waiting', timeUpdated: Date.now() - i * 1000,
      pinned: i === 24, pinnedAt: i === 24 ? 1 : 0,
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
    getSessions.mockClear();
    sessionChanged = undefined;
    sseConnect = undefined;
    useApiStore.setState({
      getSessions,
      recentSessions: [{ id: 'session-1', status: 'done' } as Session],
      recentSessionsHash: '',
    });
  });

  it('reorders background activity without a refetch and ignores older events', () => {
    useApiStore.setState({ recentSessions: [
      { id: 'first', timeUpdated: 120_000 }, { id: 'background', timeUpdated: 60_000 },
    ] as Session[] });
    const { unmount } = renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));
    act(() => sessionActivity?.('background', 180_000));
    expect(useApiStore.getState().recentSessions.map((s) => s.id)).toEqual(['background', 'first']);
    act(() => sessionActivity?.('background', 90_000));
    expect(useApiStore.getState().recentSessions[0].timeUpdated).toBe(180_000);
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
    const getSession = vi.fn(() => new Promise<{ session: Session }>((done) => { resolve = done; }));
    useApiStore.setState({ getSession: getSession as never, recentSessions: [] });
    renderHook(() => useSidebarSessions({
      id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
      abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
    }));
    act(() => { sessionActivity?.('old', 180_000); sessionActivity?.('old', 180_001); });
    expect(getSession).toHaveBeenCalledOnce();
    await act(async () => { resolve({ session: { id: 'old', timeUpdated: 1, directory: '/repo', status: 'busy' } as Session }); });
    expect(useApiStore.getState().recentSessions[0]).toMatchObject({ id: 'old', timeUpdated: 180_001 });
    expect(getSessions).not.toHaveBeenCalled();
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

  it.each(['(auto-approve subagent)', 'Research (@explore subagent)'])(
    'keeps hidden internal session %s out when SSE announces activity', async (title) => {
      const getSession = vi.fn().mockResolvedValue({ session: {
        id: 'internal', title, parentId: '', directory: '/repo', status: 'busy', timeUpdated: 1,
      } as Session });
      useApiStore.setState({ getSession, recentSessions: [], recentSessionsHash: '' });
      renderHook(() => useSidebarSessions({
        id: undefined, sessionId: undefined, collapsedProjects: [], sidebarView: 'recent',
        abortSignalRef: { current: new AbortController() }, navigate: vi.fn(),
      }));
      await act(async () => { sessionActivity?.('internal', 180_000); });
      expect(useApiStore.getState().recentSessions).toEqual([]);
      await act(async () => { sessionActivity?.('internal', 180_001); });
      expect(getSession).toHaveBeenCalledOnce();
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
