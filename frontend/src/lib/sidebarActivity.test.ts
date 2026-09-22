// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { api, type Session } from './api';
import { useApiStore } from './apiStore';
import { mergeSidebarSessions } from './sidebarHelpers';

const sessions = [
  { id: 'newer', platform: 'opencode', timeUpdated: 120_000 },
  { id: 'older', platform: 'opencode', timeUpdated: 60_000 },
] as Session[];

beforeEach(() => {
  vi.restoreAllMocks();
  useApiStore.setState({ recentSessions: sessions, recentSessionsHash: '' });
});

it('reorders immediately when an SSE patch advances activity', () => {
  useApiStore.getState().patchRecentSession('older', { timeUpdated: 180_000 });
  expect(useApiStore.getState().recentSessions.map((s) => s.id)).toEqual(['older', 'newer']);
});

it('keeps live activity and its ordering when a stale refresh completes', () => {
  const live = [{ ...sessions[1], timeUpdated: 180_000 }, sessions[0]];
  const merged = mergeSidebarSessions(sessions, live);
  expect(merged.map((s) => [s.id, s.timeUpdated])).toEqual([['older', 180_000], ['newer', 120_000]]);
});

it('keeps concurrent streams stable inside a one-minute activity bucket, including refreshes', () => {
  const store = useApiStore.getState();
  store.patchRecentSession('older', { timeUpdated: 180_001 });
  store.patchRecentSession('newer', { timeUpdated: 180_002 });
  store.patchRecentSession('older', { timeUpdated: 180_003 });
  store.patchRecentSession('newer', { timeUpdated: 239_999 });
  const live = useApiStore.getState().recentSessions;
  expect(live.map((s) => s.id)).toEqual(['older', 'newer']);
  expect(mergeSidebarSessions([...live].reverse(), live).map((s) => s.id)).toEqual(['older', 'newer']);
  store.patchRecentSession('newer', { timeUpdated: 240_000 });
  expect(useApiStore.getState().recentSessions.map((s) => s.id)).toEqual(['newer', 'older']);
});

it('moves a sent message immediately, before its request completes', async () => {
  let finish!: () => void;
  vi.spyOn(api, 'sendMessage').mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
  const sending = useApiStore.getState().sendMessage('older', 'hello');
  expect(useApiStore.getState().recentSessions[0].id).toBe('older');
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
  expect(useApiStore.getState().recentSessions[0].timeUpdated).toBe(live);
});
