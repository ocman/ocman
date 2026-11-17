import { expect, it } from 'vitest';
import type { Session } from './api';
import { mergeSessionAccounting } from './useSessionAccounting';

const s = (over: Partial<Session>) => ({ id: 'a', platform: 'opencode', title: 'Live title', status: 'busy', archived: false,
  timeUpdated: 10, messageCount: 0, durationMs: 0, totalCost: 0, ...over } as Session);

it('copies accounting totals but keeps compact status, title and archive flags', () => {
  const { sessions, pendingKeys } = mergeSessionAccounting([s({})],
    [s({ title: 'Old', status: 'done', archived: true, messageCount: 4, durationMs: 9, totalCost: 1.5, totalEffectiveCost: 2 })]);
  expect(sessions[0]).toMatchObject({ title: 'Live title', status: 'busy', archived: false, messageCount: 4, durationMs: 9, totalCost: 1.5, totalEffectiveCost: 2 });
  expect(pendingKeys.size).toBe(0);
});

it('marks missing, stale and other-owner totals as pending', () => {
  const { sessions, pendingKeys } = mergeSessionAccounting(
    [s({}), s({ id: 'b', timeUpdated: 20 }), s({ id: 'c', platform: 'remote:opencode' })],
    [s({ id: 'b', timeUpdated: 19, messageCount: 7 }), s({ id: 'c', messageCount: 3 })]);
  expect([...pendingKeys]).toEqual(['opencode:a', 'opencode:b', 'remote:opencode:c']);
  expect(sessions.map(x => x.messageCount)).toEqual([0, 0, 0]);
  expect(mergeSessionAccounting([s({})], undefined).pendingKeys.has('opencode:a')).toBe(true);
});
