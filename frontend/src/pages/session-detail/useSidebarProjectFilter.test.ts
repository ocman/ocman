// @vitest-environment jsdom
import { expect, it } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useSidebarProjectFilter } from './useSidebarProjectFilter';
import type { SidebarProjectGroup } from './SessionSidebar';

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
