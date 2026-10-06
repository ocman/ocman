// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { api, type Session } from './api';
import { useApiStore } from './apiStore';
import { computeSidebarHash, mergeSidebarSessions } from './sidebarHelpers';

const sessions = [
  { id: 'newer', platform: 'opencode', timeCreated: 2, lastTurnCompletedAt: 120_000, timeUpdated: 120_000 },
  { id: 'older', platform: 'opencode', timeCreated: 1, lastTurnCompletedAt: 60_000, timeUpdated: 60_000 },
] as Session[];

beforeEach(() => {
  vi.restoreAllMocks();
  useApiStore.setState({ recentSessions: sessions, recentSessionsHash: '' });
});

it('stores unread corrections when hidden-tab SSE already updated status and activity', () => {
  const current = [{ ...sessions[0], status: 'done', seen: true, seenTimeUpdated: 60_000, unreadCount: 0 }] as Session[];
  const store = useApiStore.getState();
  store.setRecentSessions(current, computeSidebarHash(current));
  const next = [{ ...current[0], seen: false, unreadCount: 1 }];
  const merged = mergeSidebarSessions(next, useApiStore.getState().recentSessions);
  store.setRecentSessions(merged, computeSidebarHash(merged));
  expect(useApiStore.getState().recentSessions[0]).toMatchObject({ seen: false, unreadCount: 1 });
});

it('does not reorder when an SSE patch advances streaming activity', () => {
  useApiStore.getState().patchRecentSession('older', { timeUpdated: 180_000 });
  expect(useApiStore.getState().recentSessions.map((s) => s.id)).toEqual(['newer', 'older']);
});

it('keeps live activity without using it to order a stale refresh', () => {
  const live = [{ ...sessions[1], timeUpdated: 180_000 }, sessions[0]];
  const merged = mergeSidebarSessions(sessions, live);
  expect(merged.map((s) => [s.id, s.timeUpdated])).toEqual([['newer', 120_000], ['older', 180_000]]);
});

it('keeps concurrent streams stable across minute boundaries and refreshes', () => {
  const store = useApiStore.getState();
  store.patchRecentSession('older', { timeUpdated: 180_001 });
  store.patchRecentSession('newer', { timeUpdated: 180_002 });
  store.patchRecentSession('older', { timeUpdated: 180_003 });
  store.patchRecentSession('newer', { timeUpdated: 239_999 });
  const live = useApiStore.getState().recentSessions;
  expect(live.map((s) => s.id)).toEqual(['newer', 'older']);
  expect(mergeSidebarSessions([...live].reverse(), live).map((s) => s.id)).toEqual(['newer', 'older']);
  store.patchRecentSession('newer', { timeUpdated: 240_000 });
  expect(useApiStore.getState().recentSessions.map((s) => s.id)).toEqual(['newer', 'older']);
});

it('keeps a sent message in place before its request completes', async () => {
  let finish!: () => void;
  vi.spyOn(api, 'sendMessage').mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
  const sending = useApiStore.getState().sendMessage('older', 'hello');
  expect(useApiStore.getState().recentSessions[0].id).toBe('newer');
  finish();
  await sending;
});

it('does not move a queued message until it is sent', async () => {
  vi.spyOn(api, 'sendMessage').mockResolvedValue();
  await useApiStore.getState().sendMessage('older', 'hello', undefined, undefined, undefined, undefined, undefined, true);
  expect(useApiStore.getState().recentSessions).toEqual(sessions);
});

it('restores ordering if sending fails', async () => {
  vi.spyOn(api, 'sendMessage').mockRejectedValue(new Error('offline'));
  await expect(useApiStore.getState().sendMessage('older', 'hello')).rejects.toThrow('offline');
  expect(useApiStore.getState().recentSessions).toEqual(sessions);
});

it('does not roll back newer SSE activity when a send fails', async () => {
  let fail!: (error: Error) => void;
  vi.spyOn(api, 'sendMessage').mockReturnValue(new Promise<void>((_, reject) => { fail = reject; }));
  const sending = useApiStore.getState().sendMessage('older', 'hello');
  const live = Date.now() + 100;
  useApiStore.getState().patchRecentSession('older', { timeUpdated: live });
  fail(new Error('offline'));
  await expect(sending).rejects.toThrow('offline');
  expect(useApiStore.getState().recentSessions.find(s => s.id === 'older')?.timeUpdated).toBe(live);
});

it('promotes a completed turn once and preserves its timestamp over stale polls', () => {
  const current = sessions;
  const completed = [{ ...sessions[1], lastTurnCompletedAt: 180_000, timeUpdated: 180_000 }, sessions[0]] as Session[];
  const merged = mergeSidebarSessions(completed, current);
  expect(merged.map(s => s.id)).toEqual(['older', 'newer']);
  expect(mergeSidebarSessions(sessions, merged).map(s => s.id)).toEqual(['older', 'newer']);
  const read = merged.map(s => ({ ...s, seen: true, unreadCount: 0 }));
  expect(mergeSidebarSessions(read, merged).map(s => s.id)).toEqual(['older', 'newer']);
  expect(computeSidebarHash(completed)).not.toBe(computeSidebarHash(completed.map(s => ({ ...s, lastTurnCompletedAt: 0 }))));
});

it('falls back to creation time and breaks completion ties consistently across reloads', () => {
  const rows = [
    { id: 'b', platform: 'opencode', timeCreated: 1, timeUpdated: 999, lastTurnCompletedAt: 10 },
    { id: 'a', platform: 'opencode', timeCreated: 1, timeUpdated: 2, lastTurnCompletedAt: 10 },
    { id: 'new', platform: 'opencode', timeCreated: 20, timeUpdated: 20 },
  ] as Session[];
  expect(mergeSidebarSessions(rows, []).map(s => s.id)).toEqual(['new', 'a', 'b']);
  expect(mergeSidebarSessions([...rows].reverse(), []).map(s => s.id)).toEqual(['new', 'a', 'b']);
});

it('keeps completion and read watermarks isolated across owners with matching IDs', async () => {
  const local = { ...sessions[0], id: 'shared', lastTurnCompletedAt: 300_000, seen: true, seenTimeUpdated: 300_000 };
  const remote = { ...local, platform: 'r-owner:opencode', lastTurnCompletedAt: 100_000, seen: false, seenTimeUpdated: 0 };
  const merged = mergeSidebarSessions([remote, local], [local, remote]);
  expect(merged.find(s => s.platform === remote.platform)).toMatchObject({ lastTurnCompletedAt: 100_000, seen: false, seenTimeUpdated: 0 });
  useApiStore.setState({ recentSessions: merged });
  useApiStore.getState().patchRecentSession('shared', { lastTurnCompletedAt: 400_000 }, remote.platform);
  expect(useApiStore.getState().recentSessions[0]).toMatchObject({ platform: remote.platform, lastTurnCompletedAt: 400_000 });
  expect(useApiStore.getState().recentSessions[1]).toMatchObject({ platform: local.platform, lastTurnCompletedAt: 300_000 });
  const peek = vi.spyOn(api, 'session').mockResolvedValue({ session: remote } as never);
  await useApiStore.getState().peekSession('shared', undefined, remote.platform);
  expect(peek).toHaveBeenCalledWith('shared', 1, 0, undefined, remote.platform, true);
});
