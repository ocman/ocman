import { useMemo } from 'react';
import hljs from 'highlight.js/lib/common';
import { useInfiniteRows } from '../lib/useInfiniteRows';
import { parseRawDiff } from './parseRawDiff';

// Initial render cap per file diff. Same justification as DiffView's
// cap: large diffs (e.g. lockfiles, generated code) blow up the DOM
// when several panels are open. The remainder streams in
// automatically as the sentinel enters the viewport — no click
// required.
const INITIAL_DIFF_ROWS = 500;
const DIFF_CHUNK_SIZE = 500;

// RawDiffView renders a unified-diff string (the body of a single
// `diff --git ...` section, as produced by `git diff`) using the same
// row layout as the existing inline DiffView. Line numbers are
// recovered from `@@ -A,B +C,D @@` hunk headers.
//
// We use the same .oc-diff-* CSS classes that AssistantThread.css
// already defines so this view is visually identical to the thread-
// changes diffs.

const EXTENSION_LANGUAGE_MAP: Record<string, string> = {
  ts: 'typescript', tsx: 'typescript',
  js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript',
  py: 'python', go: 'go', rs: 'rust', rb: 'ruby',
  java: 'java', kt: 'kotlin', swift: 'swift',
  c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', cxx: 'cpp', hpp: 'cpp', hxx: 'cpp',
  cs: 'csharp', php: 'php', sh: 'bash', bash: 'bash', zsh: 'bash',
  json: 'json', jsonc: 'json',
  yaml: 'yaml', yml: 'yaml',
  toml: 'toml', xml: 'xml', html: 'xml', svg: 'xml',
  md: 'markdown', markdown: 'markdown',
  css: 'css', scss: 'scss', less: 'less', sql: 'sql',
  dockerfile: 'dockerfile',
};

function inferLanguage(filePath: string): string | undefined {
  if (!filePath) return undefined;
  const name = filePath.split('/').pop() || filePath;
  if (name.toLowerCase() === 'dockerfile') return 'dockerfile';
  const m = name.match(/\.([a-zA-Z0-9]+)$/);
  if (!m) return undefined;
  return EXTENSION_LANGUAGE_MAP[m[1].toLowerCase()];
}

function highlight(code: string, language?: string): string {
  if (!code) return '';
  try {
    if (language && hljs.getLanguage(language)) {
      return hljs.highlight(code, { language, ignoreIllegals: true }).value;
    }
    return hljs.highlightAuto(code).value;
  } catch {
    return code.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c] || c));
  }
}

export interface RawDiffViewProps {
  // Unified-diff body (one file's `diff --git ...` section).
  diff: string;
  // File path used to infer syntax-highlight language. Pass empty
  // string to skip highlighting.
  filePath?: string;
}

export function RawDiffView({ diff, filePath }: RawDiffViewProps) {
  const language = useMemo(() => inferLanguage(filePath || ''), [filePath]);
  const rows = useMemo(() => parseRawDiff(diff), [diff]);
  const { visibleCount, sentinelRef, hasMore } = useInfiniteRows({
    total: rows.length,
    initial: INITIAL_DIFF_ROWS,
    chunkSize: DIFF_CHUNK_SIZE,
  });

  if (rows.length === 0) {
    return <div className="oc-diff-table oc-diff-empty">(no changes)</div>;
  }

  const visibleRows = rows.slice(0, visibleCount);

  return (
    <div className="oc-diff-table">
      {visibleRows.map((row, i) => {
        if (row.kind === 'sep') {
          return (
            <div key={i} className="oc-diff-row oc-diff-sep">
              <span className="oc-diff-ln" />
              <span className="oc-diff-ln" />
              <span className="oc-diff-code">{row.code}</span>
            </div>
          );
        }
        let cls = 'oc-diff-row';
        if (row.op === '+') cls += ' oc-diff-add';
        else if (row.op === '-') cls += ' oc-diff-del';
        return (
          <div key={i} className={cls}>
            <span className="oc-diff-ln">{row.oldLn}</span>
            <span className="oc-diff-ln">{row.newLn}</span>
            {row.code
              ? <span className="oc-diff-code" dangerouslySetInnerHTML={{ __html: highlight(row.code, language) }} />
              : <span className="oc-diff-code">{' '}</span>}
          </div>
        );
      })}
      {hasMore && (
        <div
          ref={sentinelRef}
          className="oc-diff-sentinel"
          aria-hidden="true"
        />
      )}
    </div>
  );
}
