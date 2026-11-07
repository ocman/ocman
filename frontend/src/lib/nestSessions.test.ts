import { describe, it, expect } from 'vitest';
import { nestSessions } from './nestSessions';

type Row = { id: string; parentId?: string };

const flatten = (rows: ReturnType<typeof nestSessions<Row>>) =>
  rows.map((r) => `${r.depth}:${r.session.id}`);

describe('nestSessions', () => {
  it('nests canonical subsessions across coding tools', () => {
    const rows = nestSessions([
      { id: 'ocm_parent', platform: 'tool-a' },
      { id: 'ocm_child', parentId: 'ocm_parent', platform: 'tool-b' },
      { id: 'ocm_agent', parentId: 'ocm_child', platform: 'tool-b' },
    ]);
    expect(flatten(rows)).toEqual(['0:ocm_parent', '1:ocm_child', '2:ocm_agent']);
  });
  it('keeps distinct canonical sessions and their children separate across owners', () => {
    const rows = nestSessions([
      { id: 'ocm_local_parent', platform: 'local' },
      { id: 'ocm_remote_parent', platform: 'remote' },
      { id: 'ocm_child', parentId: 'ocm_remote_parent', platform: 'remote' },
      { id: 'ocm_orphan', parentId: 'ocm_missing', platform: 'local' },
    ]);
    expect(rows.map(({ session, depth }) => `${session.platform}:${session.id}:${depth}`))
      .toEqual(['local:ocm_local_parent:0', 'remote:ocm_remote_parent:0', 'remote:ocm_child:1', 'local:ocm_orphan:0']);
  });
  it('returns [] for null/undefined/empty input', () => {
    expect(nestSessions<Row>(null)).toEqual([]);
    expect(nestSessions<Row>(undefined)).toEqual([]);
    expect(nestSessions<Row>([])).toEqual([]);
  });

  it('keeps top-level sessions flat at depth 0', () => {
    const rows = nestSessions<Row>([{ id: 'ocm_a' }, { id: 'ocm_b' }]);
    expect(flatten(rows)).toEqual(['0:ocm_a', '0:ocm_b']);
    expect(rows.every((r) => !r.hasChildren)).toBe(true);
  });

  it('places a child directly after its parent, indented', () => {
    const rows = nestSessions<Row>([
      { id: 'ocm_parent' },
      { id: 'ocm_child', parentId: 'ocm_parent' },
      { id: 'ocm_other' },
    ]);
    expect(flatten(rows)).toEqual(['0:ocm_parent', '1:ocm_child', '0:ocm_other']);
    expect(rows[0].hasChildren).toBe(true);
    expect(rows[1].hasChildren).toBe(false);
  });

  it('preserves input order among siblings', () => {
    const rows = nestSessions<Row>([
      { id: 'ocm_p' },
      { id: 'ocm_c1', parentId: 'ocm_p' },
      { id: 'ocm_c2', parentId: 'ocm_p' },
    ]);
    expect(flatten(rows)).toEqual(['0:ocm_p', '1:ocm_c1', '1:ocm_c2']);
  });

  it('nests grandchildren with increasing depth', () => {
    const rows = nestSessions<Row>([
      { id: 'ocm_a' },
      { id: 'ocm_b', parentId: 'ocm_a' },
      { id: 'ocm_c', parentId: 'ocm_b' },
    ]);
    expect(flatten(rows)).toEqual(['0:ocm_a', '1:ocm_b', '2:ocm_c']);
  });

  it('promotes orphan children (missing parent) to top level', () => {
    const rows = nestSessions<Row>([{ id: 'ocm_child', parentId: 'ocm_gone' }]);
    expect(flatten(rows)).toEqual(['0:ocm_child']);
  });

  it('treats a self-referential parent as top level', () => {
    const rows = nestSessions<Row>([{ id: 'ocm_x', parentId: 'ocm_x' }]);
    expect(flatten(rows)).toEqual(['0:ocm_x']);
  });

  it('does not drop sessions caught in a pure cycle', () => {
    const rows = nestSessions<Row>([
      { id: 'ocm_a', parentId: 'ocm_b' },
      { id: 'ocm_b', parentId: 'ocm_a' },
    ]);
    // Neither is a root, but both must still appear exactly once.
    const ids = rows.map((r) => r.session.id).sort();
    expect(ids).toEqual(['ocm_a', 'ocm_b']);
    expect(rows).toHaveLength(2);
  });
});
