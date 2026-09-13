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
  it('buckets sessions, adds empty unarchived projects, pins on top, honours saved order', () => {
    useUiStore.setState({ projectOrder: ['/repo/quiet', '/repo/a'] });
    const recentSessions = [
      session({ id: 'a1', directory: '/repo/a', timeUpdated: 10, status: 'busy' }),
      session({ id: 'a2', directory: '/repo/a', timeUpdated: 20 }),
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
    expect(archiveProject).toHaveBeenCalledWith('/repo/a', true, undefined);
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
