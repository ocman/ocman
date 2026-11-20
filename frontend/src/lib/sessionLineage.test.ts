import { describe, expect, it } from 'vitest';
import type { Session } from './api';
import { lineageKey, sessionLineage } from './sessionLineage';

const row = (id: string, overrides: Partial<Session> = {}) => ({ id, platform: 'connection-a', timeCreated: 0, ...overrides }) as Session;

describe('sessionLineage', () => {
  it('combines native and creator links while respecting owner and order', () => {
    const root = row('root');
    const current = row('current', { parentId: root.id, timeCreated: 2 });
    const sibling = row('sibling', { parentId: root.id, timeCreated: 1 });
    const child = row('child', { platform: 'connection-b', creator: { platform: current.platform, sessionId: current.id, createdAt: 3 } });
    const other = row('other', { platform: 'connection-b', parentId: current.id });
    const family = sessionLineage(current, [root, current], [current, sibling, child, other]);
    expect(family.root).toEqual(root);
    expect([...family.path]).toEqual([lineageKey(current), lineageKey(root)]);
    expect(family.children.get(lineageKey(root))).toEqual([sibling, current]);
    expect(family.children.get(lineageKey(current))).toEqual([child]);
    expect(family.unavailableParent).toBe(false);
  });

  it('reports missing native parents without falling back to a creator', () => {
    const creator = row('creator');
    const current = row('current', { parentId: 'missing', creator: { platform: creator.platform, sessionId: creator.id, createdAt: 1 } });
    const family = sessionLineage(current, [], [creator, row('missing', { platform: 'connection-b' })]);
    expect(family.root).toBe(current);
    expect(family.unavailableParent).toBe(true);
  });

  it('cuts ancestor cycles and ignores self-links', () => {
    const a = row('a', { parentId: 'b' });
    const b = row('b', { parentId: 'a' });
    const family = sessionLineage(a, [b], []);
    expect(family.root).toEqual(b);
    expect(family.children.get(lineageKey(b))).toEqual([a]);
    expect(family.children.has(lineageKey(a))).toBe(false);
    expect(sessionLineage(row('a', { parentId: 'a' }), [], []).children.size).toBe(0);
  });
});
