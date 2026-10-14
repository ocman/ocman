// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { api, type Session } from './api';
import { useApiStore } from './apiStore';
import { queryClient } from './queryClient';
import { useSessions } from './queries';
import {
  SIDEBAR_SESSIONS_KEY, getSidebarSessions, patchSessionLists, seedSessionLists, setSidebarSessions, useSidebarSessionList,
} from './sessionsCache';

const row = (id: string, extra: Partial<Session> = {}) =>
  ({ id, platform: 'opencode', directory: '/repo', status: 'done', timeCreated: 1, timeUpdated: 1, seen: true, seenTimeUpdated: 1, ...extra }) as Session;

describe('session cache', () => {
  it('shows an optimistic patch in every view that used to read its own copy', async () => {
    const sessions = [row('a'), row('b')];
    vi.spyOn(api, 'sessions').mockResolvedValue(sessions);
    // Sidebar (was apiStore.recentSessions), palette/plugin context (was
    // apiStore.cachedSessions), Dashboard and project pages (react-query).
    setSidebarSessions(sessions);
    const views = renderHook(() => ({
      sidebar: useSidebarSessionList(),
      palette: useSessions({ limit: 100 }).data,
      dashboard: useSessions({ sinceHours: 720 }).data,
      project: useSessions({ dir: '/repo', limit: 0 }).data,
    }));
    await waitFor(() => expect(views.result.current.project).toHaveLength(2));
    await waitFor(() => expect(views.result.current.palette && views.result.current.dashboard).toBeTruthy());

    act(() => patchSessionLists('a', { title: 'Renamed', status: 'busy' }));

    for (const list of Object.values(views.result.current)) {
      expect(list?.find((s) => s.id === 'a')).toMatchObject({ title: 'Renamed', status: 'busy' });
      expect(list?.find((s) => s.id === 'b')?.title).toBeUndefined();
    }
    expect(useApiStore.getState().recentStatusRevisions['opencode:a']).toBe(1);
    vi.restoreAllMocks();
  });

  it('scopes a patch to the owning platform and leaves other rows untouched', () => {
    const remote = row('a', { platform: 'r-x:opencode' });
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [row('a'), remote]);
    patchSessionLists('a', { archived: true }, 'r-x:opencode');
    expect(getSidebarSessions().map((s) => [s.platform, !!s.archived])).toEqual([['opencode', false], ['r-x:opencode', true]]);
  });

  it('never patches another owner holding the same session id', () => {
    const local = row('a');
    const remote = row('a', { platform: 'r-x:opencode' });
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [local, remote]);
    queryClient.setQueryData(['sessions', { limit: 100 }], [local, remote]);
    // Unqualified and ambiguous: nothing changes.
    patchSessionLists('a', { title: 'Renamed' });
    expect(getSidebarSessions().map((s) => s.title)).toEqual([undefined, undefined]);
    // Qualified: only that owner's rows, in every list.
    patchSessionLists('a', { title: 'Renamed' }, 'r-x:opencode');
    for (const list of [getSidebarSessions(), queryClient.getQueryData<Session[]>(['sessions', { limit: 100 }])!]) {
      expect(list.find((s) => s.platform === 'opencode')?.title).toBeUndefined();
      expect(list.find((s) => s.platform === 'r-x:opencode')?.title).toBe('Renamed');
    }
  });

  it('patches an unqualified id when a single owner holds it', () => {
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [row('a'), row('b', { platform: 'r-x:opencode' })]);
    patchSessionLists('b', { title: 'Only owner' });
    expect(getSidebarSessions().find((s) => s.id === 'b')?.title).toBe('Only owner');
  });

  it('skips identical sidebar writes and keeps the existing reference', () => {
    const first = [row('a')];
    setSidebarSessions(first);
    const stored = getSidebarSessions();
    setSidebarSessions([row('a')]);
    expect(getSidebarSessions()).toBe(stored);
    setSidebarSessions([row('a', { status: 'busy' })]);
    expect(getSidebarSessions()[0].status).toBe('busy');
  });

  it('seeds a new session into the sidebar and loaded lists, not into unloaded ones', () => {
    queryClient.setQueryData(['sessions', { limit: 100 }], [row('old')]);
    queryClient.setQueryData(['sessions', { dir: '/elsewhere' }], [row('x', { directory: '/elsewhere' })]);
    seedSessionLists(row('new'));
    expect(getSidebarSessions().map((s) => s.id)).toEqual(['new']);
    expect(queryClient.getQueryData<Session[]>(['sessions', { limit: 100 }])?.map((s) => s.id)).toEqual(['new', 'old']);
    expect(queryClient.getQueryData<Session[]>(['sessions', { dir: '/elsewhere' }])?.map((s) => s.id)).toEqual(['x']);
    expect(queryClient.getQueryData(['sessions', { sinceHours: 1 }])).toBeUndefined();
  });
});
