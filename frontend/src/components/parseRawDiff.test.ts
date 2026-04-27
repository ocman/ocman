import { describe, expect, it } from 'vitest';
import { parseRawDiff } from './parseRawDiff';

// parseRawDiff is the unified-diff reader used by the working-tree
// sidebar. It walks `git diff` output (one file's section) and emits
// one row per hunk line. These tests pin the behaviour for cases we
// exercise in production: simple modifications, additions, deletions,
// hunk separators, and the no-newline-at-end-of-file marker.

describe('parseRawDiff', () => {
  it('returns empty for empty input', () => {
    expect(parseRawDiff('')).toEqual([]);
  });

  it('skips file headers and emits one row per hunk line', () => {
    const body = [
      'diff --git a/foo.txt b/foo.txt',
      'index 0000..1111 100644',
      '--- a/foo.txt',
      '+++ b/foo.txt',
      '@@ -1,2 +1,3 @@',
      ' keep',
      '-removed',
      '+added one',
      '+added two',
      '',
    ].join('\n');
    const rows = parseRawDiff(body);
    // 1 context + 1 deletion + 2 additions = 4 rows.
    expect(rows).toHaveLength(4);
    expect(rows[0]).toMatchObject({ op: ' ', code: 'keep', oldLn: '1', newLn: '1' });
    expect(rows[1]).toMatchObject({ op: '-', code: 'removed', oldLn: '2', newLn: '' });
    expect(rows[2]).toMatchObject({ op: '+', code: 'added one', oldLn: '', newLn: '2' });
    expect(rows[3]).toMatchObject({ op: '+', code: 'added two', oldLn: '', newLn: '3' });
  });

  it('emits a separator row between hunks', () => {
    const body = [
      '@@ -1 +1 @@',
      '-old',
      '+new',
      '@@ -10,1 +10,1 @@',
      '-other',
      '+also',
    ].join('\n');
    const rows = parseRawDiff(body);
    // 2 changes + 1 sep + 2 changes = 5
    expect(rows).toHaveLength(5);
    expect(rows[2]).toMatchObject({ kind: 'sep', code: '...' });
  });

  it('renumbers correctly across the second hunk', () => {
    const body = [
      '@@ -1,1 +1,1 @@',
      '-a',
      '+A',
      '@@ -10,1 +10,1 @@',
      '-b',
      '+B',
    ].join('\n');
    const rows = parseRawDiff(body).filter((r) => r.kind === 'change');
    expect(rows[0]).toMatchObject({ op: '-', oldLn: '1' });
    expect(rows[1]).toMatchObject({ op: '+', newLn: '1' });
    expect(rows[2]).toMatchObject({ op: '-', oldLn: '10' });
    expect(rows[3]).toMatchObject({ op: '+', newLn: '10' });
  });

  it('renders the "no newline at EOF" marker as a sep row', () => {
    const body = [
      '@@ -1,1 +1,1 @@',
      '-old',
      '+new',
      '\\ No newline at end of file',
    ].join('\n');
    const rows = parseRawDiff(body);
    expect(rows[rows.length - 1]).toMatchObject({
      kind: 'sep',
      code: '\\ No newline at end of file',
    });
  });

  it('drops content before the first hunk header (file headers etc.)', () => {
    const body = [
      'diff --git a/x b/x',
      'new file mode 100644',
      '+ this should NOT be counted',
      '@@ -0,0 +1,1 @@',
      '+real',
    ].join('\n');
    const rows = parseRawDiff(body).filter((r) => r.kind === 'change');
    expect(rows).toHaveLength(1);
    expect(rows[0].code).toBe('real');
  });

  it('counts new-file additions starting from the hunk header line number', () => {
    // For a brand-new file `git diff` emits `@@ -0,0 +1,N @@`.
    // Numbering must start at 1 on the new side.
    const body = [
      '@@ -0,0 +1,3 @@',
      '+line 1',
      '+line 2',
      '+line 3',
    ].join('\n');
    const rows = parseRawDiff(body);
    expect(rows.map((r) => r.newLn)).toEqual(['1', '2', '3']);
  });
});
