import { describe, it, expect } from 'vitest';
import { nestSessions } from './nestSessions';

type Row = { id: string; parentId?: string };

const flatten = (rows: ReturnType<typeof nestSessions<Row>>) =>
  rows.map((r) => `${r.depth}:${r.session.id}`);

describe('nestSessions', () => {
  it('returns [] for null/undefined/empty input', () => {
    expect(nestSessions<Row>(null)).toEqual([]);
    expect(nestSessions<Row>(undefined)).toEqual([]);
    expect(nestSessions<Row>([])).toEqual([]);
  });

  it('keeps top-level sessions flat at depth 0', () => {
    const rows = nestSessions<Row>([{ id: 'a' }, { id: 'b' }]);
    expect(flatten(rows)).toEqual(['0:a', '0:b']);
    expect(rows.every((r) => !r.hasChildren)).toBe(true);
  });

  it('places a child directly after its parent, indented', () => {
    const rows = nestSessions<Row>([
      { id: 'parent' },
      { id: 'child', parentId: 'parent' },
      { id: 'other' },
    ]);
    expect(flatten(rows)).toEqual(['0:parent', '1:child', '0:other']);
    expect(rows[0].hasChildren).toBe(true);
    expect(rows[1].hasChildren).toBe(false);
  });

  it('preserves input order among siblings', () => {
    const rows = nestSessions<Row>([
      { id: 'p' },
      { id: 'c1', parentId: 'p' },
      { id: 'c2', parentId: 'p' },
    ]);
    expect(flatten(rows)).toEqual(['0:p', '1:c1', '1:c2']);
  });

  it('nests grandchildren with increasing depth', () => {
    const rows = nestSessions<Row>([
      { id: 'a' },
      { id: 'b', parentId: 'a' },
      { id: 'c', parentId: 'b' },
    ]);
    expect(flatten(rows)).toEqual(['0:a', '1:b', '2:c']);
  });

  it('promotes orphan children (missing parent) to top level', () => {
    const rows = nestSessions<Row>([{ id: 'child', parentId: 'gone' }]);
    expect(flatten(rows)).toEqual(['0:child']);
  });

  it('treats a self-referential parent as top level', () => {
    const rows = nestSessions<Row>([{ id: 'x', parentId: 'x' }]);
    expect(flatten(rows)).toEqual(['0:x']);
  });

  it('does not drop sessions caught in a pure cycle', () => {
    const rows = nestSessions<Row>([
      { id: 'a', parentId: 'b' },
      { id: 'b', parentId: 'a' },
    ]);
    // Neither is a root, but both must still appear exactly once.
    const ids = rows.map((r) => r.session.id).sort();
    expect(ids).toEqual(['a', 'b']);
    expect(rows).toHaveLength(2);
  });
});
