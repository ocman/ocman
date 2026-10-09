// @vitest-environment jsdom
import { expect, it } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useSidebarProjectFilter } from './useSidebarProjectFilter';
import type { SidebarProjectGroup } from './SessionSidebar';
import type { Session } from '../../lib/api';

it('distinguishes a nested project scope from a path-valued individual project', () => {
  const groups: SidebarProjectGroup[] = ['/src/org', '/src/org/child'].map((directory) => ({
    directory, sessions: [], lastUpdated: 0, aggregate: { kind: 'none' },
  }));
  const { result } = renderHook(() => useSidebarProjectFilter([], groups));
  act(() => result.current.setProjectFilter('scope:/src/org'));
  expect(result.current.sidebarProjectGroups).toEqual(groups);
  act(() => result.current.setProjectFilter('/src/org'));
  expect(result.current.sidebarProjectGroups).toEqual([groups[0]]);
});

it('filters an organization prefix using project membership, including worktrees and drafts', () => {
  const scope = '/src/github.com/nousefreak';
  const groups: SidebarProjectGroup[] = [
    { key: 'git:ocman', directory: `${scope}/ocman`, sessions: [{ id: 'worktree', platform: 'opencode', directory: '/worktrees/task' } as Session], lastUpdated: 0, aggregate: { kind: 'none' } },
    { key: 'remote:other', directory: `${scope}/other`, sessions: [{ id: 'remote', platform: 'r-box:opencode' } as Session], lastUpdated: 0, aggregate: { kind: 'none' } },
    { directory: `${scope}/draft`, sessions: [], drafts: [], lastUpdated: 0, aggregate: { kind: 'none' } },
    { directory: `${scope}-other/repo`, sessions: [{ id: 'outside', platform: 'opencode' } as Session], lastUpdated: 0, aggregate: { kind: 'none' } },
    { directory: '__pinned__', sessions: [], isPinned: true, lastUpdated: 0, aggregate: { kind: 'none' } },
  ];
  const sessions = groups.flatMap((group) => group.sessions);
  const { result } = renderHook(() => useSidebarProjectFilter(sessions, groups));
  act(() => result.current.setProjectFilter(scope));
  expect(result.current.sidebarProjectGroups).toEqual(groups.slice(0, 3));
  expect(result.current.recentSessions).toEqual(sessions.slice(0, 2));
  act(() => result.current.setProjectFilter('remote:other'));
  expect(result.current.sidebarProjectGroups).toEqual([groups[1]]);
  act(() => result.current.setProjectFilter(''));
  expect(result.current.sidebarProjectGroups).toEqual(groups);
  expect(result.current.recentSessions).toEqual(sessions);
});

it('filters empty projects and stays filtered if the selected project disappears', () => {
  const group: SidebarProjectGroup = { directory: '/empty', sessions: [], lastUpdated: 0, aggregate: { kind: 'none' } };
  const { result, rerender } = renderHook(({ groups }) => useSidebarProjectFilter([], groups), {
    initialProps: { groups: [group] },
  });
  act(() => result.current.setProjectFilter('/empty'));
  expect(result.current.sidebarProjectGroups).toEqual([group]);
  expect(result.current.recentSessions).toEqual([]);
  rerender({ groups: [] });
  expect(result.current.sidebarProjectGroups).toEqual([]);
  expect(result.current.projectFilter).toBe('/empty');
  act(() => result.current.setProjectFilter(''));
  expect(result.current.projectFilter).toBe('');
});
