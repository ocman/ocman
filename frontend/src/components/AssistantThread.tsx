import React, { useState, useEffect, useRef, useCallback, useMemo, Suspense } from 'react';
import { MultiFileDiff, PatchDiff } from '@pierre/diffs/react';
import { DIFF_OPTIONS } from './diffOptions';
import './AssistantThread.css';
import {
  ThreadPrimitive,
  MessagePrimitive,
  useMessage,
  type ToolCallMessagePartProps,
} from '@assistant-ui/react';
import { formatSeconds, formatTokensPerSecond, formatCompactNumber, formatCurrency } from '../lib/format';
import { useTurnStats } from '../lib/turnStats';
import { useAgentColor } from '../lib/agentColor';
import { useShortcut } from '../lib/shortcutRegistry';
import { hardenMessageLinks } from '../lib/linkHardener';
import { parseTodos } from '../lib/todos';
import { TodoList } from './TodoList';
import { useFailedSends } from '../lib/failedSendsContext';
import { isMutedTool, isMutedLineTool } from '../lib/mutedTools';
import { parseAnsi, hasAnsi, hasStyle, type AnsiSegment } from '../lib/ansi';
import { useStickyBottom } from '../lib/useStickyBottom';
import { trackRender } from '../lib/renderRateMonitor';
import {
  highlightDiffCode,
  extractPatchPayload,
  splitToolArgs,
  shortenPatchPath,
  summarizePatch,
  applyPatchToUnifiedFileDiffs,
  parseQuestionAnswers,
  parseQuestions,
  parseToolTime,
  formatToolDuration,
  type ApplyPatchFileDiff,
  type QuestionData,
} from '../lib/threadHelpers';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import type { FC } from 'react';
import { EmbeddedThread } from './EmbeddedThread';
import { LinkPreviewStrip } from './GitHubLinkPreview';

/**
 * Renders a duration badge for a tool card. For completed tools it
 * shows a static duration; for running tools it shows a live elapsed
 * counter that ticks every second.
 */
const ToolDuration: FC<{ startedAt: number; completedAt: number; isRunning: boolean }> = ({
  startedAt,
  completedAt,
  isRunning,
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
  if (elapsed <= 0) return null;
  return <span className="oc-tool-duration">{formatToolDuration(elapsed)}</span>;
};

function fallbackCopy(text: string) {
  const el = document.createElement('div');
  el.contentEditable = 'true';
  el.style.position = 'fixed';
  el.style.opacity = '0';
  el.innerText = text;
  document.body.appendChild(el);
  // iOS requires selecting a range inside a contenteditable element
  const range = document.createRange();
  range.selectNodeContents(el);
  const sel = window.getSelection();
  sel?.removeAllRanges();
  sel?.addRange(range);
  document.execCommand('copy');
  document.body.removeChild(el);
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function CodeBlockPre(props: any) {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { children, node: _node, ...rest } = props;
  const codeRef = useRef<HTMLPreElement>(null);
  const [copied, setCopied] = useState(false);
  const handleCopy = () => {
    const text = codeRef.current?.textContent || '';
    // Show feedback immediately — don't wait for the async clipboard promise
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text).catch(() => fallbackCopy(text));
    } else {
      fallbackCopy(text);
    }
  };
  return (
    <div className="oc-code-block">
      <button className={`oc-code-copy${copied ? ' oc-code-copy--copied' : ''}`} onClick={handleCopy} title="Copy code">
        <i className={`bi ${copied ? 'bi-check2' : 'bi-copy'}`} aria-hidden="true" />
      </button>
      <pre ref={codeRef} {...rest}>{children}</pre>
    </div>
  );
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function MarkdownLink(props: any) {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { node: _node, ...rest } = props;
  return <a {...rest} target="_blank" rel="noopener noreferrer" />;
}

// Module-scoped to keep prop references stable across renders. Fresh
// array/object literals here would invalidate react-markdown's
// internal unified-processor cache on every streaming chunk.
const REMARK_PLUGINS = [remarkGfm];
const REHYPE_PLUGINS = [rehypeHighlight];
const MARKDOWN_COMPONENTS = { pre: CodeBlockPre, a: MarkdownLink };

const MarkdownText: FC<{ text: string }> = ({ text }) => {
  if (!text.trim()) return null;
  return (
    <>
      <ReactMarkdown
        remarkPlugins={REMARK_PLUGINS}
        rehypePlugins={REHYPE_PLUGINS}
        components={MARKDOWN_COMPONENTS}
      >
        {text}
      </ReactMarkdown>
      <LinkPreviewStrip text={text} />
    </>
  );
};

const ImageDisplay: FC<{ image: string; filename?: string }> = ({ image, filename }) => {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="oc-image-wrap">
      <img
        src={image}
        alt={filename || 'Image'}
        className={`oc-image${expanded ? ' oc-image-expanded' : ''}`}
        onClick={() => setExpanded(!expanded)}
        loading="lazy"
      />
      {filename && <div className="oc-image-label">{filename}</div>}
    </div>
  );
};

const UserTextPart: FC<{ text: string }> = ({ text }) => {
  if (!text.trim()) return null;
  return (
    <>
      <span style={{ whiteSpace: 'pre-wrap' }}>{text}</span>
      <LinkPreviewStrip text={text} />
    </>
  );
};

const UserMessage: FC = () => {
  const content = useMessage((m) => m.content);
  const id = useMessage((m) => m.id);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const isQueued = custom?.queued === true;
  const agent = typeof custom?.agent === 'string' ? (custom.agent as string) : undefined;
  const failed = (custom?.failed && typeof custom.failed === 'object')
    ? (custom.failed as { error?: string; imagesDropped?: boolean })
    : undefined;
  const failedSendsCtx = useFailedSends();
  // Queued messages use their own peach accent — don't override it with the
  // agent color until the message actually starts being processed. Failed
  // sends override both with a danger-tinted border so the banner reads as
  // attached to the right bubble.
  const agentBorder = useAgentColor(agent);
  let borderStyle: React.CSSProperties | undefined;
  if (failed) {
    borderStyle = { borderLeftColor: 'var(--danger)' };
  } else if (!isQueued && agent) {
    borderStyle = { borderLeftColor: agentBorder };
  }
  const hasContent = content.some(
    (p) => (p.type === 'text' && 'text' in p && (p as { text: string }).text.trim()) || p.type === 'tool-call' || p.type === 'image'
  );
  // A failed send is worth showing even when its body is empty — the banner
  // itself carries the action the user needs.
  if (!hasContent && !failed) return null;

  return (
    <MessagePrimitive.Root
      className={`oc-msg oc-msg-user${isQueued ? ' oc-msg-queued' : ''}${failed ? ' oc-msg-failed' : ''}`}
      style={borderStyle}
    >
      <div className="oc-msg-body">
        <MessagePrimitive.Content components={USER_PART_COMPONENTS} />
      </div>
      {isQueued && !failed && (
        <div className="oc-msg-queued-badge">
          <span className="oc-queued-dot" title="Queued" />
          Queued
        </div>
      )}
      {failed && id && (
        <div className="oc-msg-failed-banner" role="alert">
          <i className="bi bi-exclamation-triangle-fill" aria-hidden="true" />
          <span className="oc-msg-failed-text">
            Failed to send{failed.error ? `: ${failed.error}` : ''}
            {failed.imagesDropped && (
              <span className="oc-msg-failed-hint"> (images couldn{'\u2019'}t be saved across refresh)</span>
            )}
          </span>
          <span className="oc-msg-failed-actions">
            <button
              type="button"
              className="oc-msg-failed-btn oc-msg-failed-retry"
              onClick={() => failedSendsCtx.retry(id)}
            >Retry</button>
            <button
              type="button"
              className="oc-msg-failed-btn oc-msg-failed-dismiss"
              onClick={() => failedSendsCtx.dismiss(id)}
              aria-label="Dismiss failed message"
            >Dismiss</button>
          </span>
        </div>
      )}
    </MessagePrimitive.Root>
  );
};

const AssistantMessage: FC = () => {
  const content = useMessage((m) => m.content);
  const messageId = useMessage((m) => m.id);
  const hasContent = content.some(
    (p) => (p.type === 'text' && 'text' in p && (p as { text: string }).text.trim()) || p.type === 'tool-call' || p.type === 'image'
  );
  if (!hasContent) return null;

  // Messages that only contain muted tool calls (reads/greps/webfetch) render as a compact list
  const onlyMuted = content.every(
    (p) => {
      if (p.type === 'text' && 'text' in p && !(p as { text: string }).text.trim()) return true;
      if (p.type !== 'tool-call' || !('toolName' in p)) return false;
      return isMutedTool((p as { toolName: string }).toolName);
    }
  );

  if (onlyMuted) {
    return (
      <MessagePrimitive.Root className="oc-msg oc-msg-muted">
        <MessagePrimitive.Content components={ASSISTANT_PART_COMPONENTS} />
        <TurnSummaryBar messageId={messageId} />
      </MessagePrimitive.Root>
    );
  }

  return (
    <MessagePrimitive.Root className="oc-msg oc-msg-assistant">
      <div className="oc-msg-body oc-md">
        <MessagePrimitive.Content components={ASSISTANT_PART_COMPONENTS} />
      </div>
      <AssistantMeta />
      <TurnSummaryBar messageId={messageId} />
    </MessagePrimitive.Root>
  );
};

function AssistantMeta() {
  const createdAt = useMessage((m) => m.createdAt);
  const status = useMessage((m) => m.status);
  const content = useMessage((m) => m.content);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const agent = typeof custom?.agent === 'string' ? (custom.agent as string) : undefined;
  if (!createdAt || createdAt.getTime() === 0) return null;
  if (status?.type === 'running') return null;
  // Hide timestamp when message only contains file reads
  const onlyReads = content.every(
    (p) => {
      if (p.type === 'text' && 'text' in p && !(p as { text: string }).text.trim()) return true;
      if (p.type !== 'tool-call' || !('toolName' in p)) return false;
      return isMutedTool((p as { toolName: string }).toolName);
    }
  );
  if (onlyReads) return null;
  const time = createdAt.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
  const isError = status?.type === 'incomplete' && 'reason' in status && status.reason === 'error';
  const errorName = custom?.errorName as string | undefined;
  const isAbort = errorName === 'MessageAbortedError' || errorName === 'AbortError';

  // Compute duration and tokens-per-second from per-message timing data when available.
  const msgTime = custom?.time as { created?: number; completed?: number } | undefined;
  const msgTokens = custom?.tokens as { output?: number } | undefined;
  let durationSec: number | null = null;
  let tps: number | null = null;
  if (msgTime?.created && msgTime?.completed) {
    const d = (msgTime.completed - msgTime.created) / 1000;
    if (d > 0) {
      durationSec = d;
      if (msgTokens?.output) tps = msgTokens.output / d;
    }
  }

  return (
    <>
      {isError && (
        <div className={`oc-error-banner${isAbort ? ' oc-error-banner-abort' : ''}`}>
          {isAbort
            ? <><i className="bi bi-slash-circle" aria-hidden="true" /> Interrupted</>
            : <><i className="bi bi-exclamation-triangle-fill" aria-hidden="true" /> Session ended with an error</>
          }
        </div>
      )}
      <div className="oc-msg-meta">
        <span
          className="oc-meta-dot"
          style={isError ? { background: 'var(--danger)' } : undefined}
          title={isError ? 'Error' : agent ? `agent: ${agent}` : 'Message group'}
        />
        <span>{time}</span>
        {durationSec !== null && (
          <>
            <span className="oc-meta-sep">·</span>
            <span className="oc-meta-tps">{formatSeconds(durationSec)}</span>
          </>
        )}
        {tps !== null && (
          <>
            <span className="oc-meta-sep">·</span>
            <span className="oc-meta-tps">{formatTokensPerSecond(tps)} tok/s</span>
          </>
        )}
      </div>
    </>
  );
}

/**
 * Always-visible summary bar rendered after the last assistant message of
 * each turn. Shows wall-clock duration, total tokens in+out, tool-call
 * count, and average tok/s. While the turn is still in progress the bar
 * shows whatever data is available so far.
 */
function TurnSummaryBar({ messageId }: { messageId: string }) {
  const stats = useTurnStats(messageId);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const agent = typeof custom?.agent === 'string' ? (custom.agent as string) : undefined;
  const agentColor = useAgentColor(agent);
  const [now, setNow] = useState(Date.now);

  // Tick every second so the live wall-clock increments visibly.
  useEffect(() => {
    if (!stats?.isLive) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [stats?.isLive]);

  if (!stats) return null;

  const { wallClockMs, tokensOut, tokensIn, cost, toolCalls, tps, isLive, startedAt } = stats;

  // For a live turn, compute elapsed wall-clock from startedAt → now.
  const displayMs = isLive ? now - startedAt : wallClockMs;
  const wallSec = displayMs !== null && displayMs > 0 ? displayMs / 1000 : null;

  const totalTokens = tokensIn + tokensOut;

  const time = new Date(startedAt).toLocaleTimeString('en-US', {
    hour: '2-digit', minute: '2-digit', hour12: false,
  });

  const items: React.ReactNode[] = [];
  if (!isLive) {
    items.push(
      <span key="time" className="oc-turn-stat">
        {time}
      </span>
    );
  }
  if (wallSec !== null) {
    items.push(
      <span key="wall" className="oc-turn-stat">
        <i className="bi bi-clock" aria-hidden="true" />
        {formatSeconds(wallSec)}
      </span>
    );
  }
  if (totalTokens > 0) {
    items.push(
      <span key="tok" className="oc-turn-stat">
        <i className="bi bi-stars" aria-hidden="true" />
        {formatCompactNumber(totalTokens)} tok
      </span>
    );
  }
  if (toolCalls > 0) {
    items.push(
      <span key="tools" className="oc-turn-stat">
        <i className="bi bi-tools" aria-hidden="true" />
        {toolCalls} {toolCalls === 1 ? 'tool' : 'tools'}
      </span>
    );
  }
  if (tps !== null) {
    items.push(
      <span key="tps" className="oc-turn-stat">
        {formatTokensPerSecond(tps)} tok/s
      </span>
    );
  }
  if (cost > 0) {
    items.push(
      <span key="cost" className="oc-turn-stat">
        {formatCurrency(cost, cost < 0.001 ? 4 : 2)}
      </span>
    );
  }

  if (items.length === 0 && !isLive) return null;

  return (
    <div className="oc-turn-stats">
      <span
        className={`oc-turn-dot${isLive ? ' oc-turn-dot--live' : ''}`}
        style={agent ? { background: agentColor } : undefined}
        title={isLive ? 'Turn in progress' : undefined}
      />
      {items.map((item, i) => (
        <React.Fragment key={i}>
          {item}
          {i < items.length - 1 && <span className="oc-turn-sep">·</span>}
        </React.Fragment>
      ))}
    </div>
  );
}

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
  /** Judge's one-line conclusion shown inline on the notice. Empty for
   *  legacy approvals or when the model omitted the field. */
  reasoning?: string;
}) {
  return (
    <div className="oc-auto-approved-notice">
      <span className="oc-auto-approved-icon" aria-hidden="true">&#10003;</span>
      <span className="oc-auto-approved-label">Auto-approved by AI</span>
      <span className="oc-auto-approved-action">{permission}</span>
      {patterns.length > 0 && (
        <span className="oc-auto-approved-patterns">
          {patterns.join(', ')}
        </span>
      )}
      {reasoning && (
        <span
          className="oc-auto-approved-reasoning"
          data-testid="auto-approved-reasoning"
          title={reasoning}
        >
          {reasoning}
        </span>
      )}
    </div>
  );
}

const ToolCallDisplay: FC<ToolCallMessagePartProps> = ({ toolName, argsText: rawArgsText, result }) => {
  const [expanded, setExpanded] = useState(false);
  const [taskExpanded, setTaskExpanded] = useState(false);

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

  // Subagent tasks render as a compact card with an embedded thread preview.
  // Clicking the header navigates to the full sub-session page.
  if (toolName === '__task__') {
    const lines = (argsText || '').split('\n');
    const taskStatus = lines[0] || 'running';
    const label = lines.slice(1).join(' ').trim() || 'Subagent task';

    let sessionId = '';
    let taskOutput = '';
    type LiveTool = { toolName: string; summary?: string; subagentId?: string; startedAt?: string };
    let liveTools: LiveTool[] = [];
    let subMessages: import('../lib/api').Message[] = [];
    let subParts: import('../lib/api').Part[] = [];
    try {
      const parsed = JSON.parse(typeof result === 'string' ? result : '{}');
      sessionId = parsed.taskId || '';
      taskOutput = (parsed.taskOutput || '').replace(/^<task_result>\n?/, '').replace(/\n?<\/task_result>$/, '').trim();
      if (Array.isArray(parsed.liveTools)) liveTools = parsed.liveTools as LiveTool[];
      if (parsed.subSession) {
        const sub = parsed.subSession as { messages?: unknown[]; parts?: unknown[] };
        if (Array.isArray(sub.messages)) subMessages = sub.messages as import('../lib/api').Message[];
        if (Array.isArray(sub.parts)) subParts = sub.parts as import('../lib/api').Part[];
      }
    } catch { /* ignore */ }

    let statusIcon = '\u2022';
    let statusClass = 'oc-tool-running';
    let statusTitle = 'Running';
    if (taskStatus === 'completed') { statusIcon = '\u2713'; statusClass = 'oc-tool-done'; statusTitle = 'Completed'; }
    else if (taskStatus === 'error') { statusIcon = '\u2717'; statusClass = 'oc-tool-error'; statusTitle = 'Error'; }

    const handleHeaderClick = sessionId ? () => { window.location.href = `/session/${sessionId}`; } : undefined;
    // Show the embedded thread when we have sub-session data.
    const hasSubSession = subMessages.length > 0;
    // Live tool list is only meaningful while the task runs and we
    // have no sub-session data or summary yet.
    const showLiveTools = taskStatus === 'running' && !hasSubSession && !taskOutput && liveTools.length > 0;

    return (
      <div className={`oc-tool oc-tool-task ${statusClass} ${taskExpanded ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header" onClick={handleHeaderClick} style={sessionId ? { cursor: 'pointer' } : undefined}>
          <span className={`oc-tool-icon ${statusClass}`} title={statusTitle}>{statusIcon}</span>
          <span className="oc-tool-label">{label}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={taskStatus === 'running'} />}
          {sessionId && <span className="oc-task-link">{'\u2197'}</span>}
        </div>
        {showLiveTools && (
          <div className="oc-tool-content">
            <ul className="oc-task-live-tools" aria-live="polite">
              {liveTools.map((t, idx) => (
                <li key={`${t.subagentId || ''}-${t.toolName}-${idx}`} className="oc-task-live-tool">
                  <span className="oc-task-live-arrow">{'\u21B3'}</span>
                  <span className="oc-task-live-name">{t.toolName}</span>
                  {t.summary && <span className="oc-task-live-summary"> {t.summary}</span>}
                </li>
              ))}
            </ul>
          </div>
        )}
        {hasSubSession && (
          <div className="oc-tool-content" onClick={() => !taskExpanded && setTaskExpanded(true)} style={!taskExpanded ? { cursor: 'pointer' } : undefined}>
            <EmbeddedThread messages={subMessages} parts={subParts} />
            {!taskExpanded && (
              <div className="oc-tool-expand">Click to expand</div>
            )}
          </div>
        )}
        {!hasSubSession && taskOutput && (
          <div className="oc-tool-content" onClick={() => !taskExpanded && setTaskExpanded(true)} style={!taskExpanded ? { cursor: 'pointer' } : undefined}>
            <div className="oc-tool-pre oc-tool-output oc-md"><MarkdownText text={taskOutput} /></div>
            {!taskExpanded && taskOutput.length > 500 && (
              <div className="oc-tool-expand">Click to expand</div>
            )}
          </div>
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
  const outputPreview = toolOutputPreview(outputDisplay, expanded);

  // Detect TodoWrite tool calls and render as a checklist
  const isTodo = toolName === 'mcp_todowrite' || toolName === 'todowrite' || toolName === 'TodoWrite';
  const todos = isTodo ? parseTodos(detail, result) : null;

  if (todos) {
    return (
      <div className={`oc-tool ${statusClass}`}>
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
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
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
          <span className={`oc-tool-icon ${statusClass}`} title={statusTitle}>{statusIcon}</span>
          <span className="oc-tool-label">{patchSummary}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
        </div>
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
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
          <i className={`bi bi-pencil-fill oc-tool-icon ${statusClass}`} title={statusTitle} aria-hidden="true" />
          <span className="oc-tool-label">{title || toolName}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
        </div>
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
    const command = detail || title;
    // Auto-expand while the command is running so output streams visibly.
    const isRunningWithOutput = toolStatus === 'running' && !!outputDisplay;
    const bashExpanded = expanded || isRunningWithOutput;
    const bashOutputDisplay = toolOutputPreview(outputDisplay, bashExpanded);
    return (
      <div className={`oc-tool oc-tool-shell ${userExecutedTool ? 'oc-tool-shell-user' : ''} ${statusClass} ${bashExpanded ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
          <i className={`bi bi-terminal-fill oc-tool-icon ${statusClass}`} title={statusTitle} aria-hidden="true" />
          <span className="oc-tool-label">{title && title !== command ? title : toolName}</span>
          {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
        </div>
        <div className="oc-tool-content" onClick={() => !bashExpanded && setExpanded(true)} style={!bashExpanded ? { cursor: 'pointer' } : undefined}>
          <pre className="oc-shell-block">
{command && <><span className="oc-shell-prompt">$</span> <span className="oc-shell-cmd">{command}</span>{bashOutputDisplay ? '\n' : ''}</>}{bashOutputDisplay && <AnsiText text={bashOutputDisplay} />}
          </pre>
          {userExecutedTool && (
            <div className="oc-shell-attribution">The following tool was executed by the user</div>
          )}
          {!bashExpanded && isLong && (
            <div className="oc-tool-expand">Click to expand</div>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className={`oc-tool ${statusClass} ${expanded ? 'oc-tool-expanded' : ''}`}>
      <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
        <span className={`oc-tool-icon ${statusClass}`} title={statusTitle}>{statusIcon}</span>
        <span className="oc-tool-label">{title || toolName}</span>
        {timeInfo && <ToolDuration startedAt={timeInfo.startedAt} completedAt={timeInfo.completedAt} isRunning={toolStatus === 'running'} />}
      </div>
      {(detail || outputDisplay) && (
        <div className="oc-tool-content" onClick={() => !expanded && setExpanded(true)} style={!expanded ? { cursor: 'pointer' } : undefined}>
          {detail && <pre className="oc-tool-pre">{detail}</pre>}
          {outputDisplay && (
            <pre className="oc-tool-pre oc-tool-output">{renderOutput(outputPreview)}</pre>
          )}
          {!expanded && isLong && (
            <div className="oc-tool-expand">
              Click to expand
            </div>
          )}
        </div>
      )}
    </div>
  );
};


// Module-scoped component maps. MessagePrimitive.Content and
// ThreadPrimitive.Messages memoize internally on the `components`
// identity — a fresh object each render busts that cache for every
// mounted message. Declared here (after all referenced FCs) to keep
// references stable across renders.
const USER_PART_COMPONENTS = { Text: UserTextPart, Image: ImageDisplay };
const ASSISTANT_PART_COMPONENTS = {
  Text: MarkdownText,
  Image: ImageDisplay,
  tools: { Fallback: ToolCallDisplay },
};
const THREAD_MESSAGE_COMPONENTS = { UserMessage, AssistantMessage };

const TOOL_OUTPUT_PREVIEW_CHARS = 5000;

function toolOutputPreview(output: string, expanded: boolean): string {
  if (expanded || output.length <= TOOL_OUTPUT_PREVIEW_CHARS) return output;
  return `${output.slice(0, TOOL_OUTPUT_PREVIEW_CHARS)}\n... (${output.length} chars total)`;
}

export function AssistantThread({ hasMore, loadingMore, onLoadMore, composer, footer }: { hasMore?: boolean; loadingMore?: boolean; onLoadMore?: () => void; composer?: React.ReactNode; footer?: React.ReactNode }) {
  trackRender('AssistantThread');
  const threadRef = useRef<HTMLDivElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const hasMoreRef = useRef(hasMore);
  const loadingMoreRef = useRef(loadingMore);
  const onLoadMoreRef = useRef(onLoadMore);
  useEffect(() => { hasMoreRef.current = hasMore; }, [hasMore]);
  useEffect(() => { loadingMoreRef.current = loadingMore; }, [loadingMore]);
  useEffect(() => { onLoadMoreRef.current = onLoadMore; }, [onLoadMore]);

  // Safety net for any non-markdown-rendered links in message bodies.
  // Markdown links are handled at render time by MarkdownLink; this catches
  // anything else that might land in the DOM with a raw href.
  //
  // Debounced via requestIdleCallback (with a 200ms timeout fallback) so
  // the link scan runs at most once per idle period instead of on every
  // DOM mutation during streaming (P1 fix).
  useEffect(() => {
    const thread = threadRef.current;
    if (!thread) return;
    const apply = () => hardenMessageLinks(thread);
    apply();
    let idleHandle: number | ReturnType<typeof setTimeout> | null = null;
    const scheduleApply = () => {
      if (idleHandle !== null) return;
      if (typeof requestIdleCallback === 'function') {
        idleHandle = requestIdleCallback(() => { idleHandle = null; apply(); }, { timeout: 200 });
      } else {
        idleHandle = setTimeout(() => { idleHandle = null; apply(); }, 200);
      }
    };
    const observer = new MutationObserver(scheduleApply);
    observer.observe(thread, { childList: true, subtree: true });
    return () => {
      observer.disconnect();
      if (idleHandle !== null) {
        if (typeof cancelIdleCallback === 'function' && typeof idleHandle === 'number') {
          cancelIdleCallback(idleHandle);
        } else {
          clearTimeout(idleHandle as ReturnType<typeof setTimeout>);
        }
      }
    };
  }, []);

  const hasAutoLoadedRef = useRef(false);
  const isJumpingRef = useRef(false);
  useEffect(() => {
    if (hasMore && !loadingMore && !hasAutoLoadedRef.current && onLoadMore) {
      hasAutoLoadedRef.current = true;
      onLoadMore();
    }
    if (!hasMore) {
      hasAutoLoadedRef.current = false;
    }
    isJumpingRef.current = false;
  }, [hasMore, loadingMore, onLoadMore]);

  // Auto-load older messages when scrolled near the top of the viewport.
  // Attaches a passive scroll listener to the viewport element.
  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    const onScroll = () => {
      if (el.scrollTop < 200 && hasMoreRef.current && !loadingMoreRef.current) {
        onLoadMoreRef.current?.();
      }
    };
    el.addEventListener('scroll', onScroll, { passive: true });
    return () => el.removeEventListener('scroll', onScroll);
  }, []);

  // Preserve scroll position when older messages are prepended
  const prevScrollHeightRef = useRef(0);
  const wasLoadingRef = useRef(false);
  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    if (loadingMore && !wasLoadingRef.current) {
      // Started loading: save current scroll height
      prevScrollHeightRef.current = el.scrollHeight;
    } else if (!loadingMore && wasLoadingRef.current && prevScrollHeightRef.current > 0) {
      // Finished loading: adjust scroll to maintain position
      requestAnimationFrame(() => {
        const diff = el.scrollHeight - prevScrollHeightRef.current;
        if (diff > 0) {
          el.scrollTop += diff;
        }
        prevScrollHeightRef.current = 0;
      });
    }
    wasLoadingRef.current = !!loadingMore;
  }, [loadingMore]);

  // Companion auto-scroll. ThreadPrimitive.Viewport's built-in
  // `autoScroll` decides "is at bottom" with a hardcoded 1px
  // tolerance, which is too strict for streaming chats: the composer
  // textarea growing or a code block reflowing during streaming
  // routinely leaves the user a few pixels above the bottom, after
  // which the library stops following new messages. useStickyBottom
  // relaxes that to ~80px so the conversation keeps tracking the
  // bottom while the user is "near" it. See lib/useStickyBottom.ts.
  useStickyBottom(viewportRef);

  // Alt+H / Alt+L (Option+H / Option+L on Mac) jump between user messages in
  // the history. Alt+H: previous user message (up). Alt+L: next user message
  // (down). The registry handles key-matching and preventDefault; this code
  // just computes the scroll target.
  //
  // Alt+L past the last user message scrolls to the very bottom of the
  // viewport so the most recent assistant response is visible — otherwise we'd
  // re-snap to the last user message and leave the response hidden below.
  const jumpToUserMessage = useCallback((direction: 'prev' | 'next') => {
    const viewport = viewportRef.current;
    if (!viewport) return;

    const userMsgs = Array.from(viewport.querySelectorAll<HTMLElement>('.oc-msg-user'));
    if (userMsgs.length === 0) {
      if (direction === 'next') {
        viewport.scrollTo({ top: viewport.scrollHeight, behavior: 'auto' });
      }
      return;
    }

    const viewportTop = viewport.getBoundingClientRect().top;
    const epsilon = 4;
    const offsets = userMsgs.map((el) => el.getBoundingClientRect().top - viewportTop);

    if (direction === 'next') {
      // Find the first message that's clearly below the current viewport position.
      // We look for messages with offset > 50px to skip any message that's currently
      // at the top and find the next one down.
      const nextIdx = offsets.findIndex((o) => o > 50);
      if (nextIdx === -1) {
        viewport.scrollTo({ top: viewport.scrollHeight, behavior: 'auto' });
        return;
      }
      const target = userMsgs[nextIdx];
      const targetTop = target.getBoundingClientRect().top - viewportTop + viewport.scrollTop;
      viewport.scrollTo({ top: Math.max(0, targetTop - 12), behavior: 'auto' });
      return;
    }

    // direction === 'prev'
    let targetIndex = -1;
    for (let i = offsets.length - 1; i >= 0; i--) {
      if (offsets[i] < -epsilon) { targetIndex = i; break; }
    }
    if (targetIndex === -1) {
      if (viewport.scrollTop > 100 && hasMoreRef.current && !loadingMoreRef.current && !isJumpingRef.current) {
        isJumpingRef.current = true;
        onLoadMoreRef.current?.();
      }
      return;
    }
    const target = userMsgs[targetIndex];
    const targetTop = target.getBoundingClientRect().top - viewportTop + viewport.scrollTop;
    viewport.scrollTo({ top: Math.max(0, targetTop - 12), behavior: 'auto' });
  }, []);

  const prevUserMessageShortcut = useMemo(() => ({
    id: 'session.prev-user-message',
    scope: 'session' as const,
    keys: { code: 'KeyH', alt: true },
    description: 'Jump to previous user message',
    runInEditable: true,
    handler: () => jumpToUserMessage('prev'),
  }), [jumpToUserMessage]);

  const nextUserMessageShortcut = useMemo(() => ({
    id: 'session.next-user-message',
    scope: 'session' as const,
    keys: { code: 'KeyL', alt: true },
    description: 'Jump to next user message',
    runInEditable: true,
    handler: () => jumpToUserMessage('next'),
  }), [jumpToUserMessage]);

  useShortcut(prevUserMessageShortcut);
  useShortcut(nextUserMessageShortcut);

  // Merge our viewportRef with the library's auto-scroll ref callback.
  // ThreadPrimitive.Viewport provides auto-scroll, isAtBottom tracking,
  // and content-resize handling out of the box.
  const setViewportRef = useCallback((el: HTMLDivElement | null) => {
    (viewportRef as React.MutableRefObject<HTMLDivElement | null>).current = el;
  }, []);

  // Track the ViewportFooter height so the scroll-to-bottom button
  // (positioned absolute inside .oc-thread) can float just above it.
  // RAF-coalesced to avoid layout thrash when the textarea resizes on
  // every keystroke.
  const footerRef = useCallback((el: HTMLDivElement | null) => {
    if (!el) return;
    let rafId = 0;
    const update = () => {
      if (rafId) return;
      rafId = requestAnimationFrame(() => {
        rafId = 0;
        const thread = el.closest('.oc-thread') as HTMLElement | null;
        if (thread) thread.style.setProperty('--oc-footer-height', `${el.offsetHeight}px`);
      });
    };
    update();
    const ro = new ResizeObserver(update);
    ro.observe(el);
  }, []);

  return (
    <div ref={threadRef} style={{ display: 'flex', flex: 1, minHeight: 0 }}>
      <ThreadPrimitive.Root className="oc-thread">
        <ThreadPrimitive.Viewport ref={setViewportRef} className="oc-thread-viewport" autoScroll>
          {hasMore && loadingMore && (
            <div className="oc-load-more">
              <span className="oc-spinner" /> Loading older messages...
            </div>
          )}
          <ThreadPrimitive.Empty>
            <div className="oc-empty">No messages yet.</div>
          </ThreadPrimitive.Empty>
          <ThreadPrimitive.Messages components={THREAD_MESSAGE_COMPONENTS} />
          {footer && <div className="oc-thread-footer">{footer}</div>}
          <ThreadPrimitive.ViewportFooter ref={footerRef} className="oc-viewport-footer">
            {composer}
          </ThreadPrimitive.ViewportFooter>
        </ThreadPrimitive.Viewport>
        <ThreadPrimitive.ScrollToBottom className="oc-scroll-btn">
          Scroll to bottom
        </ThreadPrimitive.ScrollToBottom>
      </ThreadPrimitive.Root>
    </div>
  );
}
