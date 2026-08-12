// Per-tool-call rendering: the ToolCallDisplay dispatcher plus its
// private renderers (inline diffs, patches, ANSI shell output,
// answered questions, auto-approved notices). Extracted from
// AssistantThread.tsx so thread mechanics and tool renderers grow
// independently.
import React, { useState, useEffect, Suspense } from 'react';
import { Link } from 'react-router-dom';
import { MultiFileDiff, PatchDiff } from '@pierre/diffs/react';
import { DIFF_OPTIONS } from '../diffOptions';
import { type ToolCallMessagePartProps } from '@assistant-ui/react';
import { parseTodos } from '../../lib/todos';
import { TodoList } from '../TodoList';
import { isMutedLineTool } from '../../lib/mutedTools';
import { parseAnsi, hasAnsi, hasStyle, type AnsiSegment } from '../../lib/ansi';
import { useIsPrinting } from '../../lib/useIsPrinting';
import { usePrintCollapse } from '../../lib/printCollapseContext';
import {
  highlightDiffCode,
  extractPatchPayload,
  splitToolArgs,
  summarizeToolArgs,
  shortenPatchPath,
  summarizePatch,
  applyPatchToUnifiedFileDiffs,
  parseQuestionAnswers,
  parseQuestions,
  parseToolTime,
  parseToolApprovals,
  formatToolDuration,
  type ApplyPatchFileDiff,
  type QuestionData,
  type ToolApproval,
} from '../../lib/threadHelpers';
import type { FC } from 'react';
import { MarkdownText } from './MarkdownText';

const ToolDuration: FC<{ startedAt: number; completedAt: number; isRunning: boolean; label?: string }> = ({
  startedAt,
  completedAt,
  isRunning,
  label,
}) => {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!isRunning) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [isRunning]);

  const elapsed = isRunning
    ? now - startedAt
    : completedAt > startedAt
      ? completedAt - startedAt
      : 0;
  if (label) {
    const duration = elapsed > 0 ? ` (${formatToolDuration(elapsed)})` : '';
    return <span className="oc-tool-label">{label}{duration}</span>;
  }
  if (elapsed <= 0) return null;
  return <span className="oc-tool-duration">{formatToolDuration(elapsed)}</span>;
};

const bashSpinnerFrames = ['⣾', '⣽', '⣻', '⢿', '⡿', '⣟', '⣯', '⣷'];

const BashPrompt: FC<{ running: boolean }> = ({ running }) => {
  const [frame, setFrame] = useState(0);
  useEffect(() => {
    if (!running) return;
    const id = setInterval(() => setFrame((current) => (current + 1) % bashSpinnerFrames.length), 80);
    return () => clearInterval(id);
  }, [running]);

  return <span className="oc-shell-prompt" data-testid={running ? 'bash-spinner' : undefined} title={running ? 'Running' : undefined}>{running ? bashSpinnerFrames[frame] : '$'}</span>;
};

// Structured diff payload emitted by convertMessages.ts for edit/write tools.
interface DiffPayload {
  __diff: true;
  filePath: string;
  before: string;
  after: string;
}

function parseDiffPayload(result: string | null | undefined): DiffPayload | null {
  if (!result) return null;
  try {
    const obj = JSON.parse(result);
    if (obj && obj.__diff === true) return obj as DiffPayload;
  } catch { /* not JSON */ }
  return null;
}

// Renders a before/after diff using @pierre/diffs.
function InlineDiff({ payload }: { payload: DiffPayload }) {
  const name = payload.filePath || 'file';
  return (
    <Suspense fallback={null}>
      <MultiFileDiff
        oldFile={{ name, contents: payload.before }}
        newFile={{ name, contents: payload.after }}
        options={DIFF_OPTIONS}
        disableWorkerPool
      />
    </Suspense>
  );
}

function renderOutput(text: string, languageHint?: string) {
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

function renderPatch(patchText: string) {
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

function AnsweredQuestionBlock({ questions, answers }: { questions: QuestionData[]; answers: string[] }) {
  return (
    <div className="oc-question-list">
      {questions.map((q, index) => (
        <div key={index} className="oc-question-card oc-question-answered-card">
          <div className="oc-question-text">{q.question}</div>
          <div className="oc-question-answer">{answers[index] || ''}</div>
        </div>
      ))}
    </div>
  );
}


// Renders shell output that may contain ANSI escape sequences. Falls
// back to a plain text node when no escapes are present so we don't
// pay any DOM-overhead cost on uncolored output (the common case for
// successful commands).
function AnsiText({ text }: { text: string }) {
  if (!hasAnsi(text)) return <>{text}</>;
  const segments = parseAnsi(text);
  return (
    <>
      {segments.map((seg, i) => {
        if (!hasStyle(seg)) return <React.Fragment key={i}>{seg.text}</React.Fragment>;
        return (
          <span key={i} className={ansiClassNames(seg)}>{seg.text}</span>
        );
      })}
    </>
  );
}

function ansiClassNames(seg: AnsiSegment): string {
  const classes: string[] = ['oc-ansi'];
  if (seg.fg) classes.push(`oc-ansi-fg-${seg.fg}`);
  if (seg.bg) classes.push(`oc-ansi-bg-${seg.bg}`);
  if (seg.bold) classes.push('oc-ansi-bold');
  if (seg.dim) classes.push('oc-ansi-dim');
  if (seg.italic) classes.push('oc-ansi-italic');
  if (seg.underline) classes.push('oc-ansi-underline');
  return classes.join(' ');
}

function AutoApprovedNotice({
  permission,
  patterns,
  reasoning,
}: {
  permission: string;
  patterns: string[];
  /** Judge's one-line conclusion shown on the notice. Empty for legacy
   *  approvals or when the model omitted the field. */
  reasoning?: string;
}) {
  return (
    <div className="oc-auto-approved-notice">
      <div className="oc-auto-approved-summary">
        <span className="oc-auto-approved-icon" aria-hidden="true">&#10003;</span>
        <span className="oc-auto-approved-label">Auto-approved by AI</span>
      </div>
      {permission && <div className="oc-auto-approved-action">{permission}</div>}
      {patterns.length > 0 && (
        <div className="oc-auto-approved-patterns">{patterns.join(', ')}</div>
      )}
      {reasoning && (
        <div className="oc-auto-approved-reasoning" data-testid="auto-approved-reasoning">
          {reasoning}
        </div>
      )}
    </div>
  );
}

function taskActivity(
  liveTools: { toolName: string; summary?: string }[],
  parts: import('../../lib/api').Part[],
) {
  const liveTool = liveTools.at(-1);
  if (liveTool) return `${liveTool.toolName}${liveTool.summary ? `: ${liveTool.summary}` : ''}`;

  for (let i = parts.length - 1; i >= 0; i--) {
    const data = parts[i].data;
    const part = typeof data === 'string'
      ? (() => { try { return JSON.parse(data) as import('../../lib/api').PartData; } catch { return null; } })()
      : data;
    if (!part || part.type === 'reasoning') continue;
    if (part.type === 'tool' && part.tool) {
      return part.state?.title ? `${part.tool}: ${part.state.title}` : `Using ${part.tool}`;
    }
    const text = part.text;
    if (text?.trim()) return text.replace(/\s+/g, ' ').trim();
  }
  return 'Waiting for activity...';
}

/**
 * AI auto-approval shown as a footnote under the tool call it
 * unblocked. Collapsed to a single muted line; clicking reveals the
 * judge's reasoning and the resources it covered.
 */
function AiApprovalFootnote({ approvals }: { approvals: ToolApproval[] }) {
  const [openState, setOpen] = useState(false);
  const isPrinting = useIsPrinting();
  const printCollapse = usePrintCollapse();
  const open = openState || (isPrinting && !printCollapse);
  return (
    <div className="oc-ai-approval-footnote">
      <button
        type="button"
        className="oc-ai-approval-toggle"
        aria-expanded={open}
        onClick={() => setOpen(!openState)}
      >
        Approved by AI
      </button>
      {open && (
        <div className="oc-ai-approval-detail" data-testid="ai-approval-detail">
          {approvals.map((approval, i) => (
            <div className="oc-ai-approval-entry" key={i}>
              {approval.permission && (
                <div className="oc-ai-approval-permission">{approval.permission}</div>
              )}
              {approval.patterns.length > 0 && (
                <div className="oc-ai-approval-patterns">{approval.patterns.join(', ')}</div>
              )}
              {approval.reasoning && (
                <div className="oc-ai-approval-reasoning">{approval.reasoning}</div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/**
 * Tool-call renderer. Splits the AI-approval markers off argsText and
 * renders them as a footnote below whatever the tool itself renders.
 */
export const ToolCallDisplay: FC<ToolCallMessagePartProps> = (props) => {
  const { approvals, strippedArgs } = parseToolApprovals(props.argsText || '');
  if (approvals.length === 0) return <ToolCallBody {...props} />;
  return (
    <>
      <ToolCallBody {...props} argsText={strippedArgs} />
      <AiApprovalFootnote approvals={approvals} />
    </>
  );
};

const ToolCallBody: FC<ToolCallMessagePartProps> = ({ toolName, argsText: rawArgsText, result }) => {
  const [expandedState, setExpanded] = useState(false);
  const [taskExpandedState, setTaskExpanded] = useState(false);
  // While printing / saving to PDF, force every block open so the
  // exported transcript is complete. CSS lifts the max-height caps
  // (see the @media print block in tokens.css); this additionally
  // reveals the few bodies that are gated on React state and would
  // otherwise be absent from the DOM. Length caps (toolOutputPreview)
  // still apply, so a single huge log can't balloon the PDF.
  //
  // The shared-conversation page can opt out via PrintCollapseContext so
  // the reader controls how verbose the PDF is: when collapse is
  // requested, only individually-expanded blocks print expanded.
  const isPrinting = useIsPrinting();
  const printCollapse = usePrintCollapse();
  const forcePrintExpand = isPrinting && !printCollapse;
  const expanded = expandedState || forcePrintExpand;
  const taskExpanded = taskExpandedState || forcePrintExpand;

  // Auto-approved notice — rendered inline before any timing/tool logic.
  if (toolName === 'ocman:auto-approved') {
    let permission = '';
    let patterns: string[] = [];
    let reasoning = '';
    try {
      const parsed = JSON.parse(rawArgsText || '{}') as {
        permission?: string;
        patterns?: string[];
        reasoning?: string;
      };
      permission = parsed.permission || '';
      patterns = parsed.patterns || [];
      reasoning = parsed.reasoning || '';
    } catch { /* ignore */ }
    return (
      <AutoApprovedNotice
        permission={permission}
        patterns={patterns}
        reasoning={reasoning}
      />
    );
  }

  // Extract timing data from the @time: line, if present.
  const timeInfo = parseToolTime(rawArgsText || '');
  const argsTextWithMeta = timeInfo ? timeInfo.strippedArgs : (rawArgsText || '');
  const argsLines = argsTextWithMeta.split('\n');
  const userExecutedTool = argsLines.includes('@user-executed-tool');
  const argsText = argsLines.filter((line) => line !== '@user-executed-tool').join('\n');

  // File reads/greps and Skill loads render as a muted inline line
  // with an arrow icon. Skill is here (rather than in its own branch)
  // because its renderer is byte-for-byte identical to a read: a one
  // line label, no collapsible body, no input JSON. The provider
  // builds an argsText like `Skill "create-commit"` so this branch
  // just displays whatever it gets.
  if (isMutedLineTool(toolName)) {
    return (
      <div className="oc-read-line">
        <span className="oc-read-arrow">{'\u2192'}</span>
        <span>{argsText || 'Read'}</span>
      </div>
    );
  }

  // Subagent tasks render their final result as a short markdown preview.
  if (toolName === '__task__') {
    const lines = (argsText || '').split('\n');
    const taskStatus = lines[0] || 'running';
    const label = lines.slice(1).join(' ').trim() || 'Subagent task';

    let sessionId = '';
    let taskOutput = '';
    type LiveTool = { toolName: string; summary?: string; subagentId?: string; startedAt?: string };
    let liveTools: LiveTool[] = [];
    let subParts: import('../../lib/api').Part[] = [];
    try {
      const parsed = JSON.parse(typeof result === 'string' ? result : '{}');
      sessionId = parsed.taskId || '';
      const rawTaskOutput = parsed.taskOutput || '';
      taskOutput = (rawTaskOutput.match(/<task_result(?:\s[^>]*)?>([\s\S]*?)(?:<\/task_result>|$)/i)?.[1] ?? rawTaskOutput).trim();
      if (Array.isArray(parsed.liveTools)) liveTools = parsed.liveTools as LiveTool[];
      if (parsed.subSession) {
        const sub = parsed.subSession as { parts?: unknown[] };
        if (Array.isArray(sub.parts)) subParts = sub.parts as import('../../lib/api').Part[];
      }
    } catch { /* ignore */ }

    let statusClass = 'oc-tool-running';
    let statusTitle = 'Running';
    if (taskStatus === 'completed') { statusClass = 'oc-tool-done'; statusTitle = 'Completed'; }
    else if (taskStatus === 'error') { statusClass = 'oc-tool-error'; statusTitle = 'Error'; }

    const activity = taskActivity(liveTools, subParts);

    return (
      <div className={`oc-tool oc-tool-task ${statusClass} ${taskExpanded ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header">
          <span className="oc-tool-label">{label}</span>
          <span className={`oc-task-status ${statusClass}`}>{statusTitle}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={taskStatus === 'running'} />}
          {sessionId && <Link className="oc-task-link" to={`/session/${encodeURIComponent(sessionId)}`} aria-label="Open detailed subagent session">{'\u2197'}</Link>}
        </div>
        {taskOutput ? (
          <div
            className={`oc-task-result oc-md ${taskExpanded ? 'oc-task-result-expanded' : ''}`}
            role="button"
            tabIndex={0}
            aria-expanded={taskExpanded}
            aria-label={`${taskExpanded ? 'Collapse' : 'Expand'} subagent task result`}
            onClick={() => setTaskExpanded((expanded) => !expanded)}
            onKeyDown={(event) => {
              if (event.key !== 'Enter' && event.key !== ' ') return;
              event.preventDefault();
              setTaskExpanded((expanded) => !expanded);
            }}
          >
            <MarkdownText text={taskOutput} />
          </div>
        ) : (
          <div className="oc-task-activity" data-testid="subagent-activity" title={activity} aria-live="polite">{activity}</div>
        )}
      </div>
    );
  }

  // Questions are answered in the composer slot. While pending, show a muted
  // summary. Once answered, repeat the question and answer in a compact block.
  if (toolName === '__question__') {
    const questions = parseQuestions(argsText || '');
    if (questions) {
      const answers = parseQuestionAnswers(result);
      if (answers) {
        return <AnsweredQuestionBlock questions={questions} answers={answers} />;
      }
      const count = questions.length;
      return (
        <div className="oc-read-line">
          <span className="oc-read-arrow">{'\u2192'}</span>
          <span>{`Asked ${count} question${count === 1 ? '' : 's'}`}</span>
        </div>
      );
    }
  }

  // First line of argsText is the tool's own status (completed/running/error)
  const lines = (argsText || '').split('\n');
  const toolStatus = lines[0] || 'running';
  const remainingArgs = lines.slice(1).join('\n');

  // Show tool calls that have content, are completed, or are actively running.
  // Only hide if there's truly nothing to show (no args, no result, no
  // meaningful status). "pending" and "running" states should remain visible
  // so the user can see operations in progress (e.g. "preparing to write").
  const hasArgs = remainingArgs.trim() && remainingArgs.trim() !== '{}';
  const hasResult = result && String(result).trim() && String(result).trim() !== '{}';
  const isActive = toolStatus === 'running' || toolStatus === 'pending';
  if (!hasArgs && !hasResult && !isActive && toolStatus !== 'completed') return null;

  // Empty tool calls that are still running render as a muted preparing indicator
  if (!hasArgs && !hasResult && isActive) {
    const lowerName = toolName.toLowerCase().replace(/^mcp_/, '');
    const preparingLabel =
      lowerName === 'edit' ? 'Preparing edit…' :
      lowerName === 'write' ? 'Preparing write…' :
      lowerName === 'bash' ? 'Preparing command…' :
      lowerName === 'read' ? 'Preparing read…' :
      lowerName === 'grep' ? 'Preparing search…' :
      lowerName === 'glob' ? 'Preparing file search…' :
      lowerName === 'task' ? 'Preparing task…' :
      lowerName === 'todowrite' ? 'Updating tasks…' :
      lowerName === 'webfetch' ? 'Preparing fetch…' :
      lowerName === 'question' ? 'Preparing question…' :
      `Preparing ${lowerName}…`;
    return (
      <div className="oc-read-line">
        <span className="oc-read-arrow">{'\u223C'}</span>
        <span>{preparingLabel}</span>
      </div>
    );
  }

  let statusIcon = '\u2022';
  let statusClass = 'oc-tool-running';
  let statusTitle = 'Running';
  if (toolStatus === 'completed') { statusIcon = '\u2713'; statusClass = 'oc-tool-done'; statusTitle = 'Completed'; }
  else if (toolStatus === 'error') { statusIcon = '\u2717'; statusClass = 'oc-tool-error'; statusTitle = 'Error'; }

  let outputDisplay = '';
  if (typeof result === 'string') outputDisplay = result;
  else if (result != null) outputDisplay = JSON.stringify(result, null, 2);

  const { title: parsedTitle, detail } = splitToolArgs(toolName, remainingArgs);
  const title = parsedTitle || toolName;

  const isLong = outputDisplay.length > 500 || (detail && detail.length > 300);
  // Truncation honors the *real* expand state (not the print-forced
  // one) so the 5000-char cap still applies in PDFs — printing reveals
  // collapsed blocks but does not lift the length cap on huge outputs.
  const outputPreview = toolOutputPreview(outputDisplay, expandedState);

  // Detect TodoWrite tool calls and render as a checklist
  const isTodo = toolName === 'mcp_todowrite' || toolName === 'todowrite' || toolName === 'TodoWrite';
  const todos = isTodo ? parseTodos(detail, result) : null;

  if (todos) {
    return (
      <div className={`oc-tool ${statusClass}`}>
        {/* The task list is always rendered, so this header has nothing
            to expand — it carries no toggle at all. */}
        <div className="oc-tool-header">
          <i className={`bi bi-check2-square oc-tool-icon ${statusClass}`} title={statusTitle} aria-hidden="true" />
          <span className="oc-tool-label">{title && title !== toolName ? title : 'Task list'}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
        </div>
        <div className="oc-tool-content">
          <TodoList todos={todos} />
        </div>
      </div>
    );
  }

  const isApplyPatch = toolName === 'apply_patch';
  if (isApplyPatch) {
    const patchSource = remainingArgs.trim();
    const { patchText } = extractPatchPayload(patchSource || detail);
    const patchSummary = patchText ? summarizePatch(patchText) : 'Apply patch';
    const patchBody = patchText || '';

    return (
      <div className={`oc-tool oc-tool-patch ${statusClass} ${expanded ? 'oc-tool-expanded' : ''}`}>
        <button
          type="button"
          className="oc-tool-header"
          aria-expanded={expanded}
          onClick={() => setExpanded(!expanded)}
        >
          <span className={`oc-tool-icon ${statusClass}`} title={statusTitle}>{statusIcon}</span>
          <span className="oc-tool-label">{patchSummary}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
        </button>
        {patchBody && (
          <div className="oc-tool-content" onClick={() => !expanded && setExpanded(true)} style={!expanded ? { cursor: 'pointer' } : undefined}>
            {renderPatch(patchBody)}
          </div>
        )}
      </div>
    );
  }

  // Edit / Write tools get a diff-style rendering
  const isEditTool = toolName === 'edit' || toolName === 'mcp_edit' || toolName === 'mcp_Edit';
  const isWriteTool = toolName === 'write' || toolName === 'mcp_write' || toolName === 'mcp_Write';
  if (isEditTool || isWriteTool) {
    const diffPayload = parseDiffPayload(result as string | null | undefined);
    return (
      <div className={`oc-tool oc-tool-edit ${statusClass} ${expanded || diffPayload ? 'oc-tool-expanded' : ''}`}>
        <button
          type="button"
          className="oc-tool-header"
          aria-expanded={expanded}
          onClick={() => setExpanded(!expanded)}
        >
          <i className={`bi bi-pencil-fill oc-tool-icon ${statusClass}`} title={statusTitle} aria-hidden="true" />
          <span className="oc-tool-label">{title || toolName}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
        </button>
        {(diffPayload || outputDisplay) && (
          <div className="oc-tool-content" onClick={() => !expanded && !diffPayload && setExpanded(true)} style={!expanded && !diffPayload ? { cursor: 'pointer' } : undefined}>
            {diffPayload
              ? <div className="oc-tool-output"><InlineDiff payload={diffPayload} /></div>
              : <pre className="oc-tool-pre oc-tool-output">{outputPreview}</pre>
            }
            {!expanded && !diffPayload && isLong && (
              <div className="oc-tool-expand">Click to expand</div>
            )}
          </div>
        )}
      </div>
    );
  }

  // Shell commands get a terminal-style rendering
  const isBash = toolName === 'bash' || toolName === 'mcp_bash';
  if (isBash) {
    const command = outputDisplay ? (detail || title) : title;
    const description = title && title !== command ? title : '';
    const bashOutput = outputDisplay || detail;
    const bashIsLong = shellOutputIsLong(bashOutput);
    const forcePrintCollapse = isPrinting && printCollapse;
    const bashExpanded = expanded || !bashIsLong;
    const bashOutputDisplay = bashExpanded ? bashOutput : shellOutputPreview(bashOutput);
    const toggleLabel = expanded ? 'Collapse output' : 'Show full output';
    return (
      <div className={`oc-tool oc-tool-shell ${bashExpanded ? 'oc-tool-expanded' : ''}`}>
        {!forcePrintCollapse && (
          <div className="oc-tool-content" onClick={() => !bashExpanded && setExpanded(true)} style={!bashExpanded ? { cursor: 'pointer' } : undefined}>
            <pre className="oc-shell-block" data-testid="shell-output-block">
{description && <><span className="oc-shell-description"># {description}</span>{'\n\n'}</>}{command && <><BashPrompt running={toolStatus === 'running'} /> <span className="oc-shell-cmd">{command}</span>{bashOutputDisplay ? '\n' : ''}</>}{bashOutputDisplay && <AnsiText text={bashOutputDisplay} />}
              {bashIsLong && (
                <button
                  type="button"
                  className="oc-tool-expand oc-shell-output-toggle"
                  aria-expanded={expanded}
                  onClick={(event) => {
                    event.stopPropagation();
                    setExpanded(!expanded);
                  }}
                >
                  {toggleLabel}
                </button>
              )}
            </pre>
            {userExecutedTool && (
              <div className="oc-shell-attribution">The following tool was executed by the user</div>
            )}
          </div>
        )}
      </div>
    );
  }

  // Generic / MCP tool calls render as a single muted line by default,
  // matching the look of read/grep/glob lines. Click to expand into
  // an inline panel showing the raw args and result. Errors get a red
  // accent but stay collapsed — same compact treatment as success so
  // the conversation stays scannable.
  const summary = parsedTitle || summarizeToolArgs(remainingArgs);
  const arrowIcon = toolStatus === 'running' ? '\u223C' : toolStatus === 'error' ? '\u2717' : '\u2192';
  const compactClass = [
    'oc-tool-compact',
    toolStatus === 'error' ? 'oc-tool-compact-error' : '',
    toolStatus === 'running' ? 'oc-tool-compact-running' : '',
    expanded ? 'oc-tool-compact-expanded' : '',
  ].filter(Boolean).join(' ');
  const hasBody = !!(detail || outputDisplay);
  const compactLine = (
    <>
      <span className="oc-read-arrow" aria-hidden="true" title={statusTitle}>{arrowIcon}</span>
      <span className="oc-tool-compact-name">{toolName}</span>
      {summary && <span className="oc-tool-compact-summary">{summary}</span>}
      {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
    </>
  );

  return (
    <div className={compactClass}>
      {/* A button only when there is something to reveal; an inert line
          otherwise, so nothing focusable does nothing. */}
      {hasBody ? (
        <button
          type="button"
          className="oc-tool-compact-line"
          aria-expanded={expanded}
          style={{ cursor: 'pointer' }}
          onClick={() => setExpanded(!expanded)}
        >
          {compactLine}
        </button>
      ) : (
        <div className="oc-tool-compact-line">{compactLine}</div>
      )}
      {expanded && hasBody && (
        <div className="oc-tool-compact-body">
          {detail && <pre className="oc-tool-pre">{detail}</pre>}
          {outputDisplay && (
            <pre className="oc-tool-pre oc-tool-output">{renderOutput(outputPreview)}</pre>
          )}
        </div>
      )}
    </div>
  );
};

const TOOL_OUTPUT_PREVIEW_CHARS = 5000;
const SHELL_OUTPUT_PREVIEW_LINES = 12;

function toolOutputPreview(output: string, expanded: boolean): string {
  if (expanded || output.length <= TOOL_OUTPUT_PREVIEW_CHARS) return output;
  return `${output.slice(0, TOOL_OUTPUT_PREVIEW_CHARS)}\n... (${output.length} chars total)`;
}

function shellOutputIsLong(output: string): boolean {
  return output.length > TOOL_OUTPUT_PREVIEW_CHARS || output.split('\n').length > SHELL_OUTPUT_PREVIEW_LINES;
}

function shellOutputPreview(output: string): string {
  return output.split('\n').slice(0, SHELL_OUTPUT_PREVIEW_LINES).join('\n').slice(0, TOOL_OUTPUT_PREVIEW_CHARS);
}
