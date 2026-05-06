import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import './AssistantThread.css';
import {
  ThreadPrimitive,
  MessagePrimitive,
  useMessage,
  type ToolCallMessagePartProps,
} from '@assistant-ui/react';
import { formatSeconds, formatTokensPerSecond } from '../lib/format';
import { useAgentColor } from '../lib/agentColor';
import { useShortcut } from '../lib/shortcutRegistry';
import { hardenMessageLinks } from '../lib/linkHardener';
import { parseTodos } from '../lib/todos';
import { TodoList } from './TodoList';
import { useFailedSends } from '../lib/failedSendsContext';
import { isMutedTool, isMutedLineTool } from '../lib/mutedTools';
import { useStickyBottom } from '../lib/useStickyBottom';
import {
  inferDiffLanguage,
  highlightDiffCode,
  extractPatchPayload,
  splitToolArgs,
  shortenPatchPath,
  summarizePatch,
  parseQuestionAnswers,
  parseQuestions,
  type QuestionData,
} from '../lib/threadHelpers';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import type { FC } from 'react';


// eslint-disable-next-line @typescript-eslint/no-explicit-any
function CodeBlockPre(props: any) {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { children, node: _node, ...rest } = props;
  const codeRef = useRef<HTMLPreElement>(null);
  const [copied, setCopied] = useState(false);
  const handleCopy = () => {
    const text = codeRef.current?.textContent || '';
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };
  return (
    <div className="oc-code-block">
      <button className="oc-code-copy" onClick={handleCopy} title="Copy code">
        {copied ? 'Copied' : 'Copy'}
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
    <ReactMarkdown
      remarkPlugins={REMARK_PLUGINS}
      rehypePlugins={REHYPE_PLUGINS}
      components={MARKDOWN_COMPONENTS}
    >
      {text}
    </ReactMarkdown>
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

const UserTextPart: FC<{ text: string }> = ({ text }) =>
  text.trim() ? <span style={{ whiteSpace: 'pre-wrap' }}>{text}</span> : null;

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
      </MessagePrimitive.Root>
    );
  }

  return (
    <MessagePrimitive.Root className="oc-msg oc-msg-assistant">
      <div className="oc-msg-body oc-md">
        <MessagePrimitive.Content components={ASSISTANT_PART_COMPONENTS} />
      </div>
      <AssistantMeta />
    </MessagePrimitive.Root>
  );
};

function AssistantMeta() {
  const createdAt = useMessage((m) => m.createdAt);
  const status = useMessage((m) => m.status);
  const content = useMessage((m) => m.content);
  const custom = useMessage((m) => m.metadata?.custom as Record<string, unknown> | undefined);
  const agent = typeof custom?.agent === 'string' ? (custom.agent as string) : undefined;
  const agentColor = useAgentColor(agent);
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
          style={isError ? { background: 'var(--danger)' } : agent ? { background: agentColor } : undefined}
          title={isError ? 'Error' : agent ? `agent: ${agent}` : 'Message group'}
        />
        <span>{time}</span>
        {durationSec !== null && (
          <>
            <span className="oc-meta-sep">|</span>
            <span className="oc-meta-tps">{formatSeconds(durationSec)}</span>
          </>
        )}
        {tps !== null && (
          <>
            <span className="oc-meta-sep">|</span>
            <span className="oc-meta-tps">{formatTokensPerSecond(tps)} tok/s</span>
          </>
        )}
      </div>
    </>
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

function renderPatch(patchText: string) {
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

// Compact live-streaming preview for a running subagent task. Renders the
// tail of the subagent's latest stdout in a small scroll-pinned container so
// the main thread shows progress while the final output is still being
// produced. Replaced by the final Markdown output once the task completes.
const TaskStreamPreview: FC<{ text: string }> = ({ text }) => {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [text]);
  return (
    <div className="oc-tool-stream" aria-live="polite">
      <div className="oc-tool-stream-header">
        <span className="oc-tool-stream-dot" />
        <span>streaming</span>
      </div>
      <div ref={ref} className="oc-tool-stream-body">{text}</div>
    </div>
  );
};

const ToolCallDisplay: FC<ToolCallMessagePartProps> = ({ toolName, argsText, result }) => {
  const [expanded, setExpanded] = useState(false);
  const [taskExpanded, setTaskExpanded] = useState(false);

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

  // Subagent tasks render as a compact card with output, clicking header opens the session
  if (toolName === '__task__') {
    const lines = (argsText || '').split('\n');
    const taskStatus = lines[0] || 'running';
    const label = lines.slice(1).join(' ').trim() || 'Subagent task';

    let sessionId = '';
    let taskOutput = '';
    let livePreview = '';
    type LiveTool = { toolName: string; summary?: string; subagentId?: string; startedAt?: string };
    let liveTools: LiveTool[] = [];
    try {
      const parsed = JSON.parse(typeof result === 'string' ? result : '{}');
      sessionId = parsed.taskId || '';
      taskOutput = (parsed.taskOutput || '').replace(/^<task_result>\n?/, '').replace(/\n?<\/task_result>$/, '').trim();
      if (typeof parsed.livePreview === 'string') livePreview = parsed.livePreview.trim();
      if (Array.isArray(parsed.liveTools)) liveTools = parsed.liveTools as LiveTool[];
    } catch { /* ignore */ }

    let statusIcon = '\u2022';
    let statusClass = 'oc-tool-running';
    let statusTitle = 'Running';
    if (taskStatus === 'completed') { statusIcon = '\u2713'; statusClass = 'oc-tool-done'; statusTitle = 'Completed'; }
    else if (taskStatus === 'error') { statusIcon = '\u2717'; statusClass = 'oc-tool-error'; statusTitle = 'Error'; }

    const handleHeaderClick = sessionId ? () => { window.location.href = `/session/${sessionId}`; } : undefined;
    const isLongOutput = taskOutput.length > 500;
    // Show the streaming container while the task is running and we don't yet
    // have a final output to display. Once taskOutput arrives, the final
    // markdown output replaces the streaming preview.
    const showStream = taskStatus === 'running' && !taskOutput && !!livePreview;
    // Live tool list is only meaningful while the task runs and we have no
    // final output yet; once the summary arrives it replaces the list.
    const showLiveTools = taskStatus === 'running' && !taskOutput && liveTools.length > 0;

    return (
      <div className={`oc-tool oc-tool-task ${statusClass} ${taskExpanded ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header" onClick={handleHeaderClick} style={sessionId ? { cursor: 'pointer' } : undefined}>
          <span className={`oc-tool-icon ${statusClass}`} title={statusTitle}>{statusIcon}</span>
          <span className="oc-tool-label">{label}</span>
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
        {showStream && (
          <div className="oc-tool-content">
            <TaskStreamPreview text={livePreview} />
          </div>
        )}
        {taskOutput && (
          <div className="oc-tool-content" onClick={() => !taskExpanded && setTaskExpanded(true)} style={!taskExpanded ? { cursor: 'pointer' } : undefined}>
            <div className="oc-tool-pre oc-tool-output oc-md"><MarkdownText text={taskOutput} /></div>
            {!taskExpanded && isLongOutput && (
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

  // Detect TodoWrite tool calls and render as a checklist
  const isTodo = toolName === 'mcp_todowrite' || toolName === 'todowrite' || toolName === 'TodoWrite';
  const todos = isTodo ? parseTodos(detail, result) : null;

  if (todos) {
    return (
      <div className={`oc-tool ${statusClass}`}>
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
          <i className={`bi bi-check2-square oc-tool-icon ${statusClass}`} title={statusTitle} aria-hidden="true" />
          <span className="oc-tool-label">{title && title !== toolName ? title : 'Task list'}</span>
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
    const { patchText, preamble } = extractPatchPayload(patchSource || detail);
    const patchSummary = patchText ? summarizePatch(patchText) : 'Apply patch';
    const patchBody = patchText || '';
    const patchIsLong = patchBody.length > 500;
    const fileLines = preamble.split('\n').map((line) => line.trim()).filter((line) => /^([MADRCU?!]|->)\s/.test(line));

    return (
      <div className={`oc-tool oc-tool-patch ${statusClass} ${expanded ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
          <span className={`oc-tool-icon ${statusClass}`} title={statusTitle}>{statusIcon}</span>
          <span className="oc-tool-label">{patchSummary}</span>
        </div>
        {(fileLines.length > 0 || patchBody) && (
          <div className="oc-tool-content" onClick={() => !expanded && setExpanded(true)} style={!expanded ? { cursor: 'pointer' } : undefined}>
            {fileLines.length > 0 && (
              <div className="oc-patch-files">
                {fileLines.map((line, index) => (
                  <div key={index} className="oc-patch-files-line">{line}</div>
                ))}
              </div>
            )}
            {patchBody && <div className="oc-tool-pre oc-tool-output">{renderPatch(patchBody)}</div>}
            {!expanded && patchIsLong && (
              <div className="oc-tool-expand">Click to expand</div>
            )}
          </div>
        )}
      </div>
    );
  }

  // Edit / Write tools get a diff-style rendering
  const isEditTool = toolName === 'edit' || toolName === 'mcp_edit' || toolName === 'mcp_Edit';
  const isWriteTool = toolName === 'write' || toolName === 'mcp_write' || toolName === 'mcp_Write';
  if (isEditTool || isWriteTool) {
    const diffLanguage = inferDiffLanguage(title || toolName, detail || '');
    const hasDiff = outputDisplay && outputDisplay.split('\n').some(l =>
      /^(\s*\d*)\s{2}(\s*\d*)\s{2}([+ -])\s(.*)$/.test(l)
    );
    return (
      <div className={`oc-tool oc-tool-edit ${statusClass} ${expanded || hasDiff ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
          <i className={`bi bi-pencil-fill oc-tool-icon ${statusClass}`} title={statusTitle} aria-hidden="true" />
          <span className="oc-tool-label">{title || toolName}</span>
        </div>
        {outputDisplay && (
          <div className="oc-tool-content" onClick={() => !expanded && !hasDiff && setExpanded(true)} style={!expanded && !hasDiff ? { cursor: 'pointer' } : undefined}>
            {hasDiff
              ? <div className="oc-tool-output">{renderOutput(outputDisplay, diffLanguage)}</div>
              : <pre className="oc-tool-pre oc-tool-output">{outputDisplay}</pre>
            }
            {!expanded && !hasDiff && isLong && (
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
    return (
      <div className={`oc-tool oc-tool-shell ${statusClass} ${bashExpanded ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
          <i className={`bi bi-terminal-fill oc-tool-icon ${statusClass}`} title={statusTitle} aria-hidden="true" />
          <span className="oc-tool-label">{title && title !== command ? title : toolName}</span>
        </div>
        <div className="oc-tool-content" onClick={() => !bashExpanded && setExpanded(true)} style={!bashExpanded ? { cursor: 'pointer' } : undefined}>
          <pre className="oc-shell-block">
{command && <><span className="oc-shell-prompt">$</span> <span className="oc-shell-cmd">{command}</span>{outputDisplay ? '\n' : ''}</>}{outputDisplay}
          </pre>
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
      </div>
      {(detail || outputDisplay) && (
        <div className="oc-tool-content" onClick={() => !expanded && setExpanded(true)} style={!expanded ? { cursor: 'pointer' } : undefined}>
          {detail && <pre className="oc-tool-pre">{detail}</pre>}
          {outputDisplay && (
            <pre className="oc-tool-pre oc-tool-output">{renderOutput(outputDisplay)}</pre>
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

export function AssistantThread({ hasMore, loadingMore, onLoadMore, composer, footer }: { hasMore?: boolean; loadingMore?: boolean; onLoadMore?: () => void; composer?: React.ReactNode; footer?: React.ReactNode }) {
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
