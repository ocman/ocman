import { describe, expect, it } from 'vitest';
import { groupWorkingTreeFiles } from './groupWorkingTreeFiles';
import type { WorkingTreeFile } from '../lib/api';

// groupWorkingTreeFiles splits the flat /api/git/diff response into
// the two visual sections the working-tree sidebar renders:
//
//   - "Untracked"  — files with status === 'untracked'
//   - "Changed"    — every other status (modified/added/deleted/renamed)
//
// We do this here (rather than inline in the component) so the
// grouping logic is unit-testable and the component can stay
// presentation-only. Order within each group preserves the input
// order; empty groups are dropped from the output.

function file(path: string, status: WorkingTreeFile['status']): WorkingTreeFile {
  return {
    path,
    status,
    additions: 0,
    deletions: 0,
    diff: '',
    isBinary: false,
  };
}

describe('groupWorkingTreeFiles', () => {
  it('returns no groups for an empty list', () => {
    expect(groupWorkingTreeFiles([])).toEqual([]);
  });

  it('groups untracked files into a single Untracked section', () => {
    const files = [file('a', 'untracked'), file('b', 'untracked')];
    const groups = groupWorkingTreeFiles(files);
    expect(groups).toHaveLength(1);
    expect(groups[0].id).toBe('untracked');
    expect(groups[0].label).toBe('Untracked');
    expect(groups[0].files.map((f) => f.path)).toEqual(['a', 'b']);
  });

  it('groups modified/added/deleted/renamed files into a single Changed section', () => {
    const files = [
      file('m', 'modified'),
      file('a', 'added'),
      file('d', 'deleted'),
      file('r', 'renamed'),
    ];
    const groups = groupWorkingTreeFiles(files);
    expect(groups).toHaveLength(1);
    expect(groups[0].id).toBe('changed');
    expect(groups[0].label).toBe('Changed');
    expect(groups[0].files.map((f) => f.path)).toEqual(['m', 'a', 'd', 'r']);
  });

  it('emits Changed before Untracked when both are present', () => {
    // Mirrors the screenshot's reading order: tracked changes first,
    // untracked at the bottom. Stable across renders.
    const files = [
      file('u1', 'untracked'),
      file('m1', 'modified'),
      file('u2', 'untracked'),
      file('a1', 'added'),
    ];
    const groups = groupWorkingTreeFiles(files);
    expect(groups.map((g) => g.id)).toEqual(['changed', 'untracked']);
    expect(groups[0].files.map((f) => f.path)).toEqual(['m1', 'a1']);
    expect(groups[1].files.map((f) => f.path)).toEqual(['u1', 'u2']);
  });

  it('reports the count for each group', () => {
    const files = [
      file('m1', 'modified'),
      file('m2', 'modified'),
      file('u1', 'untracked'),
    ];
    const groups = groupWorkingTreeFiles(files);
    expect(groups.find((g) => g.id === 'changed')?.files.length).toBe(2);
    expect(groups.find((g) => g.id === 'untracked')?.files.length).toBe(1);
  });
});
