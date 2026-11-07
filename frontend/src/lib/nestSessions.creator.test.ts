import { describe, expect, it } from 'vitest';
import { nestSessions } from './nestSessions';

describe('creator-linked sidebar nesting', () => {
  it('nests an independent cross-project session below its creator', () => {
    const child = { id: 'ocm_child', platform: 'opus', directory: '/worktree', creator: { platform: 'planning', sessionId: 'ocm_parent' } };
    const parent = { id: 'ocm_parent', platform: 'planning', directory: '/project' };
    expect(nestSessions([child, parent]).map(({ session, depth }) => [session.id, depth])).toEqual([['ocm_parent', 0], ['ocm_child', 1]]);
    expect(child).not.toHaveProperty('parentId');
  });

  it('does not attach to the same ID on a different machine', () => {
    const child = { id: 'ocm_child', platform: 'opus', creator: { platform: 'remote:planning', sessionId: 'ocm_parent' } };
    expect(nestSessions([child, { id: 'ocm_parent', platform: 'planning' }])[0].depth).toBe(0);
  });

  it('keeps native ancestry authoritative and handles creator cycles', () => {
    const rows = nestSessions([
      { id: 'ocm_child', platform: 'tool', parentId: 'ocm_native', creator: { platform: 'tool', sessionId: 'ocm_launcher' } },
      { id: 'ocm_native', platform: 'tool' }, { id: 'ocm_launcher', platform: 'tool' },
    ]);
    expect(rows.map(({ session, depth }) => [session.id, depth])).toEqual([['ocm_native', 0], ['ocm_child', 1], ['ocm_launcher', 0]]);
    const cyclic = nestSessions([
      { id: 'ocm_a', creator: { platform: '', sessionId: 'ocm_b' } },
      { id: 'ocm_b', creator: { platform: '', sessionId: 'ocm_a' } },
    ]);
    expect(cyclic.map(({ depth }) => depth)).toEqual([0, 1]);
  });
});
