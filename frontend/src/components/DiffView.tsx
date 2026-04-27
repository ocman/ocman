import { useMemo } from 'react';
import hljs from 'highlight.js/lib/common';
import { simpleDiff } from '../lib/diff';
import { useInfiniteRows } from '../lib/useInfiniteRows';
import { parseDiff } from './parseDiff';

// Initial render cap per diff. Files larger than this only mount the
// first INITIAL_DIFF_ROWS rows; the rest stream in as the user
// scrolls toward the bottom of the diff (see useInfiniteRows). The
// cap matters most when several large files are open at once — a
// typical agent session edits a handful of files but each "before"
// snapshot can be thousands of lines.
const INITIAL_DIFF_ROWS = 500;
// Number of additional rows revealed each time the bottom sentinel
// enters the viewport.
const DIFF_CHUNK_SIZE = 500;

// Extension -> highlight.js language. Kept small and explicit; the
// fallback path uses hljs.highlightAuto for anything not listed.
const EXTENSION_LANGUAGE_MAP: Record<string, string> = {
  ts: 'typescript', tsx: 'typescript',
  js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript',
  py: 'python',
  go: 'go',
  rs: 'rust',
  rb: 'ruby',
  java: 'java',
  kt: 'kotlin',
  swift: 'swift',
  c: 'c', h: 'c',
  cpp: 'cpp', cc: 'cpp', cxx: 'cpp', hpp: 'cpp', hxx: 'cpp',
  cs: 'csharp',
  php: 'php',
  sh: 'bash', bash: 'bash', zsh: 'bash',
  json: 'json', jsonc: 'json',
  yaml: 'yaml', yml: 'yaml',
  toml: 'toml',
  xml: 'xml', html: 'xml', svg: 'xml',
  md: 'markdown', markdown: 'markdown',
  css: 'css', scss: 'scss', less: 'less',
  sql: 'sql',
  dockerfile: 'dockerfile',
};

function inferLanguageFromPath(filePath: string): string | undefined {
  if (!filePath) return undefined;
  const name = filePath.split('/').pop() || filePath;
  if (name.toLowerCase() === 'dockerfile') return 'dockerfile';
  const extMatch = name.match(/\.([a-zA-Z0-9]+)$/);
  if (!extMatch) return undefined;
  return EXTENSION_LANGUAGE_MAP[extMatch[1].toLowerCase()];
}

// DiffView renders a unified diff between `before` and `after` as a
// table of (oldLine, newLine, op, code) rows. The CSS classes
// (oc-diff-table, oc-diff-row, oc-diff-add/del, oc-diff-sep,
// oc-diff-ln, oc-diff-code) are shared with the inline-thread diff
// rendering in AssistantThread.css so both surfaces look identical.
//
// The component lazily computes the diff string with simpleDiff
// (which already handles context lines + the "..." separator). For
// large files (m+n > 200) simpleDiff falls back to a flat removed/
// added view — no LCS — which is fine for our use case.
//
// `filePath` is used only to infer a syntax-highlight language for
// the code spans; pass an empty string to skip highlighting.
//
// We intentionally use dangerouslySetInnerHTML for the highlighted
// code spans because hljs returns escaped HTML. The input strings are
// the file contents we already trust enough to render in the inline
// thread view via the same path.
export interface DiffViewProps {
  before: string;
  after: string;
  filePath?: string;
  startLine?: number;
}

// Highlight a code fragment using hljs. The same pattern is used by
// the inline-thread diff renderer in AssistantThread.tsx.
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

export function DiffView({ before, after, filePath, startLine = 1 }: DiffViewProps) {
  const language = useMemo(() => inferLanguageFromPath(filePath || ''), [filePath]);
  const rows = useMemo(() => parseDiff(simpleDiff(before, after, startLine)), [before, after, startLine]);
  // Mount rows lazily as the user scrolls toward the bottom of the
  // diff. The sentinel is rendered after the last visible row;
  // attaching its ref here lets us observe it without a separate
  // wrapper component.
  const { visibleCount, sentinelRef, hasMore } = useInfiniteRows({
    total: rows.length,
    initial: INITIAL_DIFF_ROWS,
    chunkSize: DIFF_CHUNK_SIZE,
  });

  if (rows.length === 0) {
    // No changes to show — happens when before === after, which can
    // occur if the aggregator captured an edit whose final state
    // matches the initial state. The backend filters most of these
    // out; this branch is a defensive fallback.
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
              <span className="oc-diff-code">...</span>
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
          // Sentinel: when this enters the viewport, useInfiniteRows
          // reveals the next chunk. A 1px tall element is enough for
          // IntersectionObserver and stays invisible to the user.
          ref={sentinelRef}
          className="oc-diff-sentinel"
          aria-hidden="true"
        />
      )}
    </div>
  );
}
