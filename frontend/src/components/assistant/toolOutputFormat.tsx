// Non-component helpers for tool output: diff payload parsing, read/diff
// and patch formatting, and preview truncation.
import { Suspense } from 'react';
import { PatchDiff } from '@pierre/diffs/react';
import { DIFF_OPTIONS } from '../diffOptions';
import {
  highlightDiffCode,
  shortenPatchPath,
  applyPatchToUnifiedFileDiffs,
  type ApplyPatchFileDiff,
} from '../../lib/threadHelpers';

// Structured diff payload emitted by convertMessages.ts for edit/write tools.
export interface DiffPayload {
  __diff: true;
  filePath: string;
  before: string;
  after: string;
}

export function parseDiffPayload(result: string | null | undefined): DiffPayload | null {
  if (!result) return null;
  try {
    const obj = JSON.parse(result);
    if (obj && obj.__diff === true) return obj as DiffPayload;
  } catch { /* not JSON */ }
  return null;
}

export function renderOutput(text: string, languageHint?: string) {
  // Detect file read output: various XML formats from MCP read tools
  // Handles: <path>...</path> with optional <type>...</type> and <content>...</content>
  // Also handles truncated output where </content> may be missing
  const fileMatch = text.match(/<path>([^<]+)<\/path>/);
  const contentMatch = text.match(/<content>\n?([\s\S]*?)(?:\n?<\/content>|$)/);
  if (fileMatch && contentMatch) {
    const content = contentMatch[1];
    const lines = content.split('\n').map(l => l.replace(/^\d+: /, ''));
    return (
      <>
        {lines.map((line, i) => <span key={i}>{line}{'\n'}</span>)}
      </>
    );
  }

  // Detect diff output (lines with line numbers + op marker)
  const diffLines = text.split('\n');
  // Match format: " 1   2    content" or "         ..."
  const diffPattern = /^(\s*\d*)\s{2}(\s*\d*)\s{2}([+ -])\s(.*)$/;
  const hasDiff = diffLines.some(l => diffPattern.test(l));
  if (!hasDiff) return text;

  return (
    <div className="oc-diff-table">
      {diffLines.map((line, i) => {
        const m = line.match(diffPattern);
        if (!m) {
          // Context separator (...)
          if (line.trim() === '...') {
            return (
              <div key={i} className="oc-diff-row oc-diff-sep">
                <span className="oc-diff-ln" />
                <span className="oc-diff-ln" />
                <span className="oc-diff-code">...</span>
              </div>
            );
          }
          return null;
        }
        const [, oldLn, newLn, op, code] = m;
        let cls = 'oc-diff-row';
        if (op === '+') cls += ' oc-diff-add';
        else if (op === '-') cls += ' oc-diff-del';
        return (
          <div key={i} className={cls}>
            <span className="oc-diff-ln">{oldLn.trim()}</span>
            <span className="oc-diff-ln">{newLn.trim()}</span>
            {code
              ? <span className="oc-diff-code" dangerouslySetInnerHTML={{ __html: highlightDiffCode(code, languageHint) }} />
              : <span className="oc-diff-code">{' '}</span>}
          </div>
        );
      })}
    </div>
  );
}

function patchActionMeta(action: ApplyPatchFileDiff['action']): { label: string; text: string } {
  switch (action) {
    case 'add': return { label: 'A', text: 'Added' };
    case 'delete': return { label: 'D', text: 'Deleted' };
    case 'rename': return { label: 'R', text: 'Renamed' };
    case 'update': return { label: 'M', text: 'Modified' };
  }
}

export function renderPatch(patchText: string) {
  const fileDiffs = applyPatchToUnifiedFileDiffs(patchText);
  if (fileDiffs.length > 0) {
    return (
      <Suspense fallback={null}>
        <div className="oc-patch-diffs">
          {fileDiffs.map((file, index) => {
            const meta = patchActionMeta(file.action);
            return (
              <div key={`${file.action}:${file.oldPath || ''}:${file.path}:${index}`} className="oc-patch-diff-file">
                <div className={`oc-patch-diff-header oc-patch-diff-header-${file.action}`}>
                  <span className="oc-patch-diff-badge">{meta.label}</span>
                  <span className="oc-patch-diff-action">{meta.text}</span>
                  <span className="oc-patch-diff-path">
                    {file.oldPath ? `${shortenPatchPath(file.oldPath)} -> ${shortenPatchPath(file.path)}` : shortenPatchPath(file.path)}
                  </span>
                </div>
                <PatchDiff
                  patch={file.patch}
                  options={{ ...DIFF_OPTIONS, disableFileHeader: true }}
                  disableWorkerPool
                />
              </div>
            );
          })}
        </div>
      </Suspense>
    );
  }

  const lines = patchText.split('\n');
  return (
    <div className="oc-patch-block">
      {lines.map((line, i) => {
        let cls = 'oc-patch-line';
        if (/^\*\*\* (Add|Update|Delete) File: /.test(line)) cls += ' oc-patch-file';
        else if (line.startsWith('*** Begin Patch') || line.startsWith('*** End Patch') || line.startsWith('*** Move to:')) cls += ' oc-patch-meta';
        else if (line.startsWith('@@')) cls += ' oc-patch-hunk';
        else if (line.startsWith('+')) cls += ' oc-patch-add';
        else if (line.startsWith('-')) cls += ' oc-patch-del';

        const fileMatch = line.match(/^\*\*\* (Add|Update|Delete) File: (.+)$/);
        const displayLine = fileMatch ? `*** ${fileMatch[1]} File: ${shortenPatchPath(fileMatch[2])}` : line;

        return <div key={i} className={cls}>{displayLine || ' '}</div>;
      })}
    </div>
  );
}

const TOOL_OUTPUT_PREVIEW_CHARS = 5000;
const SHELL_OUTPUT_PREVIEW_LINES = 12;

export function toolOutputPreview(output: string, expanded: boolean): string {
  if (expanded || output.length <= TOOL_OUTPUT_PREVIEW_CHARS) return output;
  return `${output.slice(0, TOOL_OUTPUT_PREVIEW_CHARS)}\n... (${output.length} chars total)`;
}

export function shellOutputIsLong(output: string): boolean {
  return output.length > TOOL_OUTPUT_PREVIEW_CHARS || output.split('\n').length > SHELL_OUTPUT_PREVIEW_LINES;
}

export function shellOutputPreview(output: string): string {
  return output.split('\n').slice(0, SHELL_OUTPUT_PREVIEW_LINES).join('\n').slice(0, TOOL_OUTPUT_PREVIEW_CHARS);
}
