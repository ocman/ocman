// Parser for one file's `git diff` body. Walks unified-diff output
// from `diff --git ...` (the head, hunk headers like
// "@@ -A,B +C,D @@", and content lines prefixed with `+`, `-`, ` `)
// and emits structured rows the renderer consumes.
//
// Lives in its own module to satisfy
// react-refresh/only-export-components and to keep tests free of
// React imports.

export interface DiffRow {
  kind: 'change' | 'sep';
  oldLn: string;
  newLn: string;
  op: ' ' | '+' | '-';
  code: string;
}

const HUNK_HEADER_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

export function parseRawDiff(body: string): DiffRow[] {
  if (!body) return [];
  const rows: DiffRow[] = [];
  let oldLine = 0;
  let newLine = 0;
  let inHunk = false;
  let firstHunk = true;

  for (const line of body.split('\n')) {
    const hunkMatch = HUNK_HEADER_RE.exec(line);
    if (hunkMatch) {
      if (!firstHunk) {
        rows.push({ kind: 'sep', oldLn: '', newLn: '', op: ' ', code: '...' });
      }
      firstHunk = false;
      oldLine = parseInt(hunkMatch[1], 10);
      newLine = parseInt(hunkMatch[3], 10);
      inHunk = true;
      continue;
    }
    if (!inHunk) continue;
    // Skip metadata lines that may interleave (rare but possible
    // for combined diffs we don't handle here).
    if (line.startsWith('diff --git ') || line.startsWith('index ') ||
        line.startsWith('--- ') || line.startsWith('+++ ') ||
        line.startsWith('new file mode ') || line.startsWith('deleted file mode ') ||
        line.startsWith('rename from ') || line.startsWith('rename to ') ||
        line.startsWith('similarity index ')) {
      inHunk = false;
      continue;
    }

    if (line.startsWith('+')) {
      rows.push({ kind: 'change', oldLn: '', newLn: String(newLine), op: '+', code: line.slice(1) });
      newLine++;
    } else if (line.startsWith('-')) {
      rows.push({ kind: 'change', oldLn: String(oldLine), newLn: '', op: '-', code: line.slice(1) });
      oldLine++;
    } else if (line.startsWith(' ')) {
      rows.push({ kind: 'change', oldLn: String(oldLine), newLn: String(newLine), op: ' ', code: line.slice(1) });
      oldLine++;
      newLine++;
    } else if (line === '') {
      // Trailing newline at end of body — git often emits a blank
      // line after the last hunk. Ignore.
    } else if (line.startsWith('\\')) {
      // "\ No newline at end of file" — informational. Render as
      // a sep so the reader sees it without distorting line counts.
      rows.push({ kind: 'sep', oldLn: '', newLn: '', op: ' ', code: line });
    } else {
      // Unknown — drop silently rather than corrupting the row stream.
    }
  }
  return rows;
}
