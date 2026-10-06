// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

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

import type { Project, Session } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';

const projects: Project[] = [
  { directory: '/repo/quiet', lastUsed: 5, archived: false } as Project,
  { directory: '/repo/hidden', lastUsed: 6, archived: true } as Project,
];
const refetch = vi.fn(async () => undefined);
vi.mock('../../lib/queries', () => ({
  useProjects: () => ({ data: projects, refetch }),
}));

import { useSidebarProjectGroups } from './useSidebarProjectGroups';

const session = (over: Partial<Session>): Session => ({
  id: 'x', platform: 'opencode', directory: '/repo/a', title: 't', status: 'done',
  timeUpdated: 1, pinned: false, pinnedAt: 0, ...over,
} as Session);

describe('useSidebarProjectGroups', () => {
  it('keeps the local action target when only a remote checkout has recent sessions', () => {
    projects.push({ directory: '/local/repo', projectKey: 'git:shared' } as Project,
      { directory: '/remote/clone', remoteId: 'other', projectKey: 'git:shared' } as Project);
    try {
      const { result } = renderHook(() => useSidebarProjectGroups({ id: undefined, displayStatus: 'done', recentSessions: [
        session({ directory: '/remote/clone', remoteId: 'other', remoteName: 'Other', platform: 'r-other:opencode' }),
      ] }));
      expect(result.current.sidebarProjectGroups.find(g => g.key === 'git:shared')).toMatchObject({
        directory: '/local/repo', remoteId: 'local', remoteName: undefined, platform: undefined,
      });
    } finally {
      projects.splice(-2);
    }
  });
  it('groups same-path checkouts across hosts and archives each explicit owner', async () => {
    const shared = [
      { directory: '/local/repo', projectKey: 'git:shared', lastUsed: 1 },
      { directory: '/local/repo', remoteId: 'other', projectKey: 'git:shared', lastUsed: 2 },
    ] as Project[];
    projects.push(...shared);
    const archiveProject = vi.fn(async () => ({ ok: true }));
    useApiStore.setState({ archiveProject });
    try {
      const recentSessions = [
        session({ id: 'local', directory: '/local/repo' }),
        session({ id: 'remote', directory: '/local/repo', remoteId: 'other', platform: 'r-other:opencode' }),
      ];
      const { result } = renderHook(() => useSidebarProjectGroups({ id: 'local', recentSessions, displayStatus: 'done' }));
      const groups = result.current.sidebarProjectGroups.filter(g => g.sessions.length);
      expect(groups).toHaveLength(1);
      expect(groups[0]).toMatchObject({ key: 'git:shared', directory: '/local/repo', remoteId: 'local' });
      expect(groups[0].sessions).toHaveLength(2);
      await act(async () => { result.current.handleArchiveProjectFromSidebar('/local/repo'); });
      expect(archiveProject).toHaveBeenCalledWith('/local/repo', true, 'local');
      expect(archiveProject).toHaveBeenCalledWith('/local/repo', true, 'other');
      expect(result.current.sidebarProjectGroups.some(g => g.key === 'git:shared')).toBe(false);
    } finally {
      projects.splice(-shared.length);
    }
  });

  it('keeps unrelated sessions at identical paths on different owners separate', () => {
    const { result } = renderHook(() => useSidebarProjectGroups({ id: undefined, displayStatus: 'done', recentSessions: [
      session({ id: 'local' }), session({ id: 'remote', remoteId: 'other' }),
    ] }));
    const groups = result.current.sidebarProjectGroups.filter(g => g.sessions.length);
    expect(groups).toHaveLength(2);
    expect(new Set(groups.map(g => g.key)).size).toBe(2);
  });
  it('buckets sessions, adds empty unarchived projects, pins on top, honours saved order', () => {
    useUiStore.setState({ projectOrder: ['/repo/quiet', '/repo/a'] });
    const recentSessions = [
      session({ id: 'a1', directory: '/repo/a', timeCreated: 1, timeUpdated: 60_000, status: 'busy' }),
      session({ id: 'a2', directory: '/repo/a', timeCreated: 2, timeUpdated: 120_000 }),
      session({ id: 'b1', directory: '/repo/b', timeUpdated: 5, pinned: true, pinnedAt: 1 }),
    ];
    const { result } = renderHook(() =>
      useSidebarProjectGroups({ id: 'a1', recentSessions, displayStatus: 'waiting' }));

    const groups = result.current.sidebarProjectGroups;
    expect(groups.map((g) => g.directory)).toEqual(['__pinned__', '/repo/quiet', '/repo/a', '/repo/b']);
    expect(groups[0].isPinned).toBe(true);
    expect(groups[1].sessions).toEqual([]);
    expect(groups[2].sessions.map((s) => s.id)).toEqual(['a2', 'a1']);
    // The active row's status is layered over the group rollup.
    expect(groups[2].aggregate).toMatchObject({ kind: 'waiting' });
  });

  it('orders by completion while retaining exact activity through streaming updates', () => {
    const recentSessions = [
      { ...session({ id: 'first', timeUpdated: 60_001 }), lastTurnCompletedAt: 20 },
      { ...session({ id: 'second', timeUpdated: 119_999 }), lastTurnCompletedAt: 10 },
    ];
    const { result, rerender } = renderHook(({ recentSessions }) =>
      useSidebarProjectGroups({ id: undefined, recentSessions, displayStatus: 'done' }), { initialProps: { recentSessions } });
    const group = result.current.sidebarProjectGroups.find((g) => g.directory === '/repo/a')!;
    expect(group.sessions.map((s) => s.id)).toEqual(['first', 'second']);
    expect(group.lastUpdated).toBe(119_999);
    rerender({ recentSessions: [recentSessions[0], { ...recentSessions[1], timeUpdated: 180_000 }] });
    expect(result.current.sidebarProjectGroups.find(g => g.directory === '/repo/a')!.sessions.map(s => s.id)).toEqual(['first', 'second']);
  });

  it('reorders without the pinned pseudo-group and hides archived projects optimistically', async () => {
    useUiStore.setState({ projectOrder: [] });
    const archiveProject = vi.fn(async () => ({ ok: true }));
    useApiStore.setState({ archiveProject });
    const recentSessions = [session({ id: 'a1', directory: '/repo/a' })];
    const { result } = renderHook(() =>
      useSidebarProjectGroups({ id: 'a1', recentSessions, displayStatus: 'done' }));

    act(() => result.current.handleReorderProjects(['__pinned__', '/repo/a', '']));
    expect(useUiStore.getState().projectOrder).toEqual(['/repo/a']);

    await act(async () => { result.current.handleArchiveProjectFromSidebar('/repo/a'); });
    expect(archiveProject).toHaveBeenCalledWith('/repo/a', true, 'local');
    expect(refetch).toHaveBeenCalled();
    expect(result.current.sidebarProjectGroups.map((g) => g.directory)).toEqual(['/repo/quiet']);
  });

  it('reverts the optimistic hide when archiving fails', async () => {
    useUiStore.setState({ projectOrder: [] });
    useApiStore.setState({ archiveProject: vi.fn(async () => { throw new Error('nope'); }) });
    const recentSessions = [session({ id: 'a1', directory: '/repo/a' })];
    const { result } = renderHook(() =>
      useSidebarProjectGroups({ id: 'a1', recentSessions, displayStatus: 'done' }));

    await act(async () => { result.current.handleArchiveProjectFromSidebar('/repo/a'); });
    expect(result.current.sidebarProjectGroups.map((g) => g.directory)).toContain('/repo/a');
  });
});
