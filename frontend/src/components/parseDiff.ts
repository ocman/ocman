// Parser for the numbered-line format produced by lib/diff::simpleDiff.
// Each content line looks like "<oldLn>  <newLn>  <op> <text>" with
// bare "..." marking gaps where unchanged context was elided.
//
// Lives in its own module (rather than next to DiffView) so test
// imports don't pull React; also satisfies the
// react-refresh/only-export-components lint rule.

export interface DiffRow {
  kind: 'change' | 'sep';
  oldLn: string;
  newLn: string;
  op: string;
  code: string;
}

const DIFF_LINE_RE = /^(\s*\d*)\s{2}(\s*\d*)\s{2}([+ -])\s(.*)$/;

export function parseDiff(diff: string): DiffRow[] {
  const rows: DiffRow[] = [];
  for (const line of diff.split('\n')) {
    const m = line.match(DIFF_LINE_RE);
    if (!m) {
      if (line.trim() === '...') {
        rows.push({ kind: 'sep', oldLn: '', newLn: '', op: ' ', code: '...' });
      }
      continue;
    }
    rows.push({
      kind: 'change',
      oldLn: m[1].trim(),
      newLn: m[2].trim(),
      op: m[3],
      code: m[4],
    });
  }
  return rows;
}
