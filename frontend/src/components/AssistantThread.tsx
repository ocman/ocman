import { useState, useEffect, useRef, useCallback, memo } from 'react';
import {
  ThreadPrimitive,
  MessagePrimitive,
  useMessage,
  type ToolCallMessagePartProps,
} from '@assistant-ui/react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import type { FC } from 'react';
import { getDraft, saveDraft, clearDraft } from '../lib/composerDraft';
import { useApiStore } from '../lib/apiStore';
import { api, type SlashCommand } from '../lib/api';


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

const MarkdownText: FC<{ text: string }> = ({ text }) => {
  if (!text.trim()) return null;
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      rehypePlugins={[rehypeHighlight]}
      components={{ pre: CodeBlockPre }}
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

const UserMessage: FC = () => {
  const content = useMessage((m) => m.content);
  const isQueued = useMessage((m) => {
    const meta = m.metadata as Record<string, unknown> | undefined;
    const custom = meta?.custom as Record<string, unknown> | undefined;
    return custom?.queued === true;
  });
  const hasContent = content.some(
    (p) => (p.type === 'text' && 'text' in p && (p as { text: string }).text.trim()) || p.type === 'tool-call' || p.type === 'image'
  );
  if (!hasContent) return null;

  return (
    <MessagePrimitive.Root className={`oc-msg oc-msg-user${isQueued ? ' oc-msg-queued' : ''}`}>
      <div className="oc-msg-body">
        <MessagePrimitive.Content
          components={{
            Text: ({ text }) => text.trim() ? <span style={{ whiteSpace: 'pre-wrap' }}>{text}</span> : null,
            Image: ImageDisplay,
          }}
        />
      </div>
      {isQueued && (
        <div className="oc-msg-queued-badge">
          <span className="oc-queued-dot" title="Queued" />
          Queued
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
      const name = (p as { toolName: string }).toolName;
      return name === '__read__' || name === 'read' || name === 'mcp_read' || name === 'grep' || name === 'mcp_grep' || name === 'glob' || name === 'mcp_glob' || name === 'webfetch' || name === 'mcp_webfetch' || name === 'mcp_Webfetch';
    }
  );

  if (onlyMuted) {
    return (
      <MessagePrimitive.Root className="oc-msg oc-msg-muted">
        <MessagePrimitive.Content
          components={{
            Text: MarkdownText,
            Image: ImageDisplay,
            tools: { Fallback: ToolCallDisplay },
          }}
        />
      </MessagePrimitive.Root>
    );
  }

  return (
    <MessagePrimitive.Root className="oc-msg oc-msg-assistant">
      <div className="oc-msg-body oc-md">
        <MessagePrimitive.Content
          components={{
            Text: MarkdownText,
            Image: ImageDisplay,
            tools: { Fallback: ToolCallDisplay },
          }}
        />
      </div>
      <AssistantMeta />
    </MessagePrimitive.Root>
  );
};

function AssistantMeta() {
  const createdAt = useMessage((m) => m.createdAt);
  const status = useMessage((m) => m.status);
  const content = useMessage((m) => m.content);
  if (!createdAt || createdAt.getTime() === 0) return null;
  if (status?.type === 'running') return null;
  // Hide timestamp when message only contains file reads
  const onlyReads = content.every(
    (p) => {
      if (p.type === 'text' && 'text' in p && !(p as { text: string }).text.trim()) return true;
      if (p.type !== 'tool-call' || !('toolName' in p)) return false;
      const name = (p as { toolName: string }).toolName;
      return name === '__read__' || name === 'read' || name === 'mcp_read' || name === 'grep' || name === 'mcp_grep' || name === 'glob' || name === 'mcp_glob' || name === 'webfetch' || name === 'mcp_webfetch' || name === 'mcp_Webfetch';
    }
  );
  if (onlyReads) return null;
  const time = createdAt.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
  const isError = status?.type === 'incomplete' && 'reason' in status && status.reason === 'error';
  return (
    <>
      {isError && (
        <div className="oc-error-banner" style={{ marginTop: 10 }}>
          Session ended with an error
        </div>
      )}
      <div className="oc-msg-meta">
        <span className="oc-meta-dot" style={isError ? { background: 'var(--danger)' } : undefined} title={isError ? 'Error' : 'Message group'} />
        <span>{time}</span>
      </div>
    </>
  );
}

function renderOutput(text: string) {
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
            <span className="oc-diff-code">{code}</span>
          </div>
        );
      })}
    </div>
  );
}

interface PatchSection {
  action: 'add' | 'update' | 'delete';
  path: string;
}

interface PatchPayload {
  patchText: string | null;
  preamble: string;
}

function parseJsonObject(text: string): Record<string, unknown> | null {
  const trimmed = text.trim();
  if (!trimmed.startsWith('{') || !trimmed.endsWith('}')) return null;
  try {
    const parsed = JSON.parse(trimmed);
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, unknown> : null;
  } catch {
    return null;
  }
}

function parseJsonObjectFromMixedText(text: string): Record<string, unknown> | null {
  const start = text.indexOf('{');
  const end = text.lastIndexOf('}');
  if (start < 0 || end <= start) return null;
  return parseJsonObject(text.slice(start, end + 1));
}

function extractPatchPayload(text: string): PatchPayload {
  const parsed = parseJsonObject(text) || parseJsonObjectFromMixedText(text);
  if (typeof parsed?.patchText === 'string') {
    const jsonStart = text.indexOf('{');
    const preamble = jsonStart > 0 ? text.slice(0, jsonStart).trim() : '';
    return { patchText: parsed.patchText, preamble };
  }

  return { patchText: null, preamble: text.trim() };
}

function splitToolArgs(toolName: string, rawArgs: string): { title: string; detail: string } {
  const argLines = rawArgs.split('\n');
  const firstLine = argLines[0] || '';
  const rest = argLines.slice(1).join('\n').trim();

  // Some tools, notably apply_patch, send structured JSON without a title.
  // In that case the first line is part of the payload, not a display label.
  if (toolName === 'apply_patch' && (firstLine.trim().startsWith('{') || !rest)) {
    return { title: '', detail: rawArgs.trim() };
  }

  return { title: firstLine, detail: rest };
}

function parsePatchSections(patchText: string): PatchSection[] {
  return patchText.split('\n').flatMap<PatchSection>((line) => {
    const updateMatch = line.match(/^\*\*\* Update File: (.+)$/);
    if (updateMatch) return [{ action: 'update' as const, path: updateMatch[1] }];
    const addMatch = line.match(/^\*\*\* Add File: (.+)$/);
    if (addMatch) return [{ action: 'add' as const, path: addMatch[1] }];
    const deleteMatch = line.match(/^\*\*\* Delete File: (.+)$/);
    if (deleteMatch) return [{ action: 'delete' as const, path: deleteMatch[1] }];
    return [];
  });
}

function summarizePatch(patchText: string): string {
  const sections = parsePatchSections(patchText);
  if (sections.length === 0) return 'Patch';
  if (sections.length === 1) {
    const section = sections[0];
    const action = section.action === 'add' ? 'Add' : section.action === 'delete' ? 'Delete' : 'Update';
    return `${action} ${shortenPatchPath(section.path)}`;
  }

  const counts = sections.reduce<{ add: number; update: number; delete: number }>((acc, section) => {
    acc[section.action] += 1;
    return acc;
  }, { add: 0, update: 0, delete: 0 });
  const parts = [
    counts.add ? `${counts.add} add${counts.add === 1 ? '' : 's'}` : null,
    counts.update ? `${counts.update} update${counts.update === 1 ? '' : 's'}` : null,
    counts.delete ? `${counts.delete} delete${counts.delete === 1 ? '' : 's'}` : null,
  ].filter(Boolean);
  return `Patch ${sections.length} files${parts.length ? ` (${parts.join(', ')})` : ''}`;
}

function shortenPatchPath(path: string): string {
  if (!path.startsWith('/')) return path;

  const markers = ['/frontend/', '/internal/', '/docs/', '/.github/'];
  for (const marker of markers) {
    const index = path.indexOf(marker);
    if (index >= 0) return path.slice(index + 1);
  }

  const parts = path.split('/').filter(Boolean);
  return parts.slice(-2).join('/');
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

interface QuestionOption {
  label: string;
  description: string;
}

interface QuestionData {
  header: string;
  question: string;
  options: QuestionOption[];
}

function parseQuestionAnswers(result: unknown): string[] | null {
  if (typeof result !== 'string' || !result.trim()) return null;

  const normalizeAnswer = (value: string): string => {
    const trimmed = value.trim();
    const mappedMatch = trimmed.match(/=\s*"([^"]+)"/);
    if (mappedMatch) return mappedMatch[1].trim();
    const quotedMatch = trimmed.match(/^"([^"]+)"$/);
    if (quotedMatch) return quotedMatch[1].trim();
    return trimmed;
  };

  // The result may be JSON-stringified multiple times (e.g. a JSON string
  // inside another JSON string). Unwrap up to two levels.
  const unwrap = (raw: string): unknown => {
    try {
      const first = JSON.parse(raw);
      if (typeof first === 'string') {
        try { return JSON.parse(first); } catch { return first; }
      }
      return first;
    } catch { return raw; }
  };

  const parsed = unwrap(result);

  if (Array.isArray(parsed)) {
    const answers = parsed.map((entry) => {
      if (Array.isArray(entry)) return entry.join(', ').trim();
      if (typeof entry === 'string') return normalizeAnswer(entry);
      if (entry && typeof entry === 'object') {
        // Handle {label: "..."} or {answer: "..."} shaped objects
        const obj = entry as Record<string, unknown>;
        const val = obj.label || obj.answer || obj.value || obj.text;
        if (typeof val === 'string') return normalizeAnswer(val);
        return JSON.stringify(entry);
      }
      return '';
    }).filter(Boolean);
    return answers.length > 0 ? answers : null;
  }
  if (typeof parsed === 'string' && parsed.trim()) return [normalizeAnswer(parsed)];

  // Fallback for non-JSON result
  if (typeof result === 'string') {
    const raw = normalizeAnswer(result);
    if (raw && raw !== '""' && raw !== '[]') return [raw];
  }
  return null;
}

function parseQuestions(argsText: string): QuestionData[] | null {
  // argsText format: "status\njsonData"
  const lines = argsText.split('\n');
  const jsonStr = lines.slice(1).join('\n').trim();
  if (!jsonStr) return null;
  try {
    const parsed = JSON.parse(jsonStr);
    // Could be { questions: [...] } or just [...]
    const questions = parsed?.questions || parsed;
    if (Array.isArray(questions) && questions.length > 0 && questions[0]?.question) {
      return questions as QuestionData[];
    }
  } catch { /* not JSON */ }
  return null;
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

interface TodoItem {
  content: string;
  status: string;
  priority: string;
}

function parseTodos(argsText: string, result: unknown): TodoItem[] | null {
  // Try to extract todos from the tool args or result
  const sources = [argsText, typeof result === 'string' ? result : JSON.stringify(result)];
  for (const src of sources) {
    if (!src) continue;
    try {
      const parsed = JSON.parse(src);
      const todos = parsed?.todos || parsed;
      if (Array.isArray(todos) && todos.length > 0 && todos[0]?.content && todos[0]?.status) {
        return todos as TodoItem[];
      }
    } catch {
      // Try to find JSON within the string (may have prefix lines)
      const jsonStart = src.indexOf('[');
      const jsonEnd = src.lastIndexOf(']');
      if (jsonStart >= 0 && jsonEnd > jsonStart) {
        try {
          const todos = JSON.parse(src.slice(jsonStart, jsonEnd + 1));
          if (Array.isArray(todos) && todos.length > 0 && todos[0]?.content && todos[0]?.status) {
            return todos as TodoItem[];
          }
        } catch { /* not JSON */ }
      }
    }
  }
  return null;
}

function TodoList({ todos }: { todos: TodoItem[] }) {
  return (
    <div className="oc-todo-list">
      {todos.map((t, i) => {
        const isDone = t.status === 'completed';
        const isActive = t.status === 'in_progress';
        let cls = 'oc-todo-item';
        if (isDone) cls += ' oc-todo-done';
        if (isActive) cls += ' oc-todo-active';
        return (
          <div key={i} className={cls}>
            <span className="oc-todo-check" title={isDone ? 'Completed' : isActive ? 'In progress' : 'Pending'}>{isDone ? '\u2713' : isActive ? '\u25B6' : '\u25CB'}</span>
            <span className="oc-todo-text">{t.content}</span>
            {t.priority === 'high' && <span className="oc-todo-priority" title="High priority">!</span>}
          </div>
        );
      })}
    </div>
  );
}

const ToolCallDisplay: FC<ToolCallMessagePartProps> = ({ toolName, argsText, result }) => {
  const [expanded, setExpanded] = useState(false);
  const [taskExpanded, setTaskExpanded] = useState(false);

  // File reads/greps render as a muted inline line with an arrow icon
  if (toolName === '__read__' || toolName === 'read' || toolName === 'mcp_read' || toolName === 'grep' || toolName === 'mcp_grep' || toolName === 'glob' || toolName === 'mcp_glob' || toolName === 'webfetch' || toolName === 'mcp_webfetch' || toolName === 'mcp_Webfetch') {
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
    try {
      const parsed = JSON.parse(typeof result === 'string' ? result : '{}');
      sessionId = parsed.taskId || '';
      taskOutput = parsed.taskOutput || '';
    } catch { /* ignore */ }

    let statusIcon = '\u2022';
    let statusClass = 'oc-tool-running';
    let statusTitle = 'Running';
    if (taskStatus === 'completed') { statusIcon = '\u2713'; statusClass = 'oc-tool-done'; statusTitle = 'Completed'; }
    else if (taskStatus === 'error') { statusIcon = '\u2717'; statusClass = 'oc-tool-error'; statusTitle = 'Error'; }

    const handleHeaderClick = sessionId ? () => { window.location.href = `/session/${sessionId}`; } : undefined;
    const isLongOutput = taskOutput.length > 500;

    return (
      <div className={`oc-tool oc-tool-task ${statusClass} ${taskExpanded ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header" onClick={handleHeaderClick} style={sessionId ? { cursor: 'pointer' } : undefined}>
          <span className={`oc-tool-icon ${statusClass}`} title={statusTitle}>{statusIcon}</span>
          <span className="oc-tool-label">{label}</span>
          {sessionId && <span className="oc-task-link">{'\u2197'}</span>}
        </div>
        {taskOutput && (
          <div className="oc-tool-content" onClick={() => !taskExpanded && setTaskExpanded(true)} style={!taskExpanded ? { cursor: 'pointer' } : undefined}>
            <pre className="oc-tool-pre oc-tool-output">{taskOutput}</pre>
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

   // Shell commands get a terminal-style rendering
  const isBash = toolName === 'bash' || toolName === 'mcp_bash';
  if (isBash) {
    const command = detail || title;
    return (
      <div className={`oc-tool oc-tool-shell ${statusClass} ${expanded ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
          <i className={`bi bi-terminal-fill oc-tool-icon ${statusClass}`} title={statusTitle} aria-hidden="true" />
          <span className="oc-tool-label">{title && title !== command ? title : toolName}</span>
        </div>
        <div className="oc-tool-content" onClick={() => !expanded && setExpanded(true)} style={!expanded ? { cursor: 'pointer' } : undefined}>
          <pre className="oc-shell-block">
{command && <><span className="oc-shell-prompt">$</span> <span className="oc-shell-cmd">{command}</span>{outputDisplay ? '\n' : ''}</>}{outputDisplay}
          </pre>
          {!expanded && isLong && (
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

// Encode raw PCM Float32 samples into a WAV Blob (works on all browsers).
function encodeWav(samples: Float32Array, sampleRate: number): Blob {
  const numSamples = samples.length;
  const buffer = new ArrayBuffer(44 + numSamples * 2);
  const view = new DataView(buffer);

  function writeString(offset: number, str: string) {
    for (let i = 0; i < str.length; i++) view.setUint8(offset + i, str.charCodeAt(i));
  }

  writeString(0, 'RIFF');
  view.setUint32(4, 36 + numSamples * 2, true);
  writeString(8, 'WAVE');
  writeString(12, 'fmt ');
  view.setUint32(16, 16, true);            // chunk size
  view.setUint16(20, 1, true);             // PCM
  view.setUint16(22, 1, true);             // mono
  view.setUint32(24, sampleRate, true);     // sample rate
  view.setUint32(28, sampleRate * 2, true); // byte rate
  view.setUint16(32, 2, true);             // block align
  view.setUint16(34, 16, true);            // bits per sample
  writeString(36, 'data');
  view.setUint32(40, numSamples * 2, true);

  // Convert float32 [-1,1] to int16
  let offset = 44;
  for (let i = 0; i < numSamples; i++, offset += 2) {
    const s = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7FFF, true);
  }

  return new Blob([buffer], { type: 'audio/wav' });
}

// Recording state held outside React to avoid ref/state complexity.
interface RecordingCtx {
  stream: MediaStream;
  audioCtx: AudioContext;
  processor: ScriptProcessorNode;
  chunks: Float32Array[];
}

export interface AttachedImage {
  url: string;  // data URL (base64)
  mime: string;
}

// Read a File as a data URL
function readFileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

// Known context window sizes (in tokens) for common models.
// Used to compute percentage used. Partial matches are supported (matched from the end).
const MODEL_CONTEXT_WINDOWS: Record<string, number> = {
  // OpenAI
  'gpt-4o': 128_000,
  'gpt-4o-mini': 128_000,
  'gpt-4-turbo': 128_000,
  'gpt-4': 8_192,
  'gpt-4.1': 1_047_576,
  'gpt-4.1-mini': 1_047_576,
  'gpt-4.1-nano': 1_047_576,
  'o1': 200_000,
  'o1-mini': 128_000,
  'o1-pro': 200_000,
  'o3': 200_000,
  'o3-mini': 200_000,
  'o3-pro': 200_000,
  'o4-mini': 200_000,
  // Anthropic
  'claude-sonnet-4-20250514': 200_000,
  'claude-opus-4-20250514': 200_000,
  'claude-opus-4-6-20250616': 200_000,
  'claude-3-7-sonnet-20250219': 200_000,
  'claude-3-5-sonnet-20241022': 200_000,
  'claude-3-5-sonnet-20240620': 200_000,
  'claude-3-5-haiku-20241022': 200_000,
  'claude-3-opus-20240229': 200_000,
  'claude-3-haiku-20240307': 200_000,
  'claude-sonnet-4': 200_000,
  'claude-opus-4-6': 200_000,
  'claude-opus-4': 200_000,
  // Google
  'gemini-2.5-pro': 1_048_576,
  'gemini-2.5-flash': 1_048_576,
  'gemini-2.0-flash': 1_048_576,
  'gemini-1.5-pro': 2_097_152,
  'gemini-1.5-flash': 1_048_576,
  // xAI
  'grok-3': 131_072,
  'grok-3-mini': 131_072,
  // DeepSeek
  'deepseek-chat': 64_000,
  'deepseek-reasoner': 64_000,
  // Mistral
  'mistral-large-latest': 128_000,
  'codestral-latest': 256_000,
};

function getContextWindow(modelId: string | undefined): number | null {
  if (!modelId) return null;
  // Exact match first
  if (MODEL_CONTEXT_WINDOWS[modelId]) return MODEL_CONTEXT_WINDOWS[modelId];
  // Try partial match (model ID may have provider prefix like "anthropic/claude-...")
  // Sort by key length descending so more specific models match first
  // (e.g. "gpt-4.1-mini" before "gpt-4")
  const lower = modelId.toLowerCase();
  const sorted = Object.entries(MODEL_CONTEXT_WINDOWS).sort((a, b) => b[0].length - a[0].length);
  for (const [key, value] of sorted) {
    if (lower.endsWith(key) || lower.includes(key)) return value;
  }
  return null;
}

function formatTokenCount(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return String(n);
}

// Known user-selectable agents in OpenCode.
const KNOWN_AGENTS = ['build', 'developer', 'plan', 'architect', 'ba', 'brainstormer', 'reviewer', 'security'];

function Composer({
  onSend,
  onCommand,
  onAbort,
  isRunning,
  disabled,
  whisperAvailable,
  models,
  activeModel,
  selectedModel,
  onModelChange,
  activeAgent,
  selectedAgent,
  onAgentChange,
  contextTokens,
  sessionId,
  directory,
}: {
  onSend?: (text: string, images?: AttachedImage[]) => void;
  onCommand?: (command: string, args: string) => void;
  onAbort?: () => void;
  isRunning: boolean;
  disabled?: boolean;
  whisperAvailable?: boolean;
  models?: string[];
  activeModel?: string;
  selectedModel?: string;
  onModelChange?: (model: string) => void;
  activeAgent?: string;
  selectedAgent?: string;
  onAgentChange?: (agent: string) => void;
  contextTokens?: number;
  sessionId?: string;
  directory?: string;
}) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const micRef = useRef<HTMLButtonElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const onSendRef = useRef(onSend);
  const isRunningRef = useRef(isRunning);
  const disabledRef = useRef(disabled);
  const mountedRef = useRef(false);
  const recordingRef = useRef<RecordingCtx | null>(null);
  const [images, setImages] = useState<AttachedImage[]>([]);
  const imagesRef = useRef<AttachedImage[]>([]);
  const sessionIdRef = useRef(sessionId);
  const draftTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const onAbortRef = useRef(onAbort);

  // Keep refs in sync
  useEffect(() => { imagesRef.current = images; }, [images]);
  useEffect(() => { sessionIdRef.current = sessionId; }, [sessionId]);
  useEffect(() => {
    onSendRef.current = onSend;
  }, [onSend]);
  useEffect(() => {
    onAbortRef.current = onAbort;
  }, [onAbort]);
  useEffect(() => {
    isRunningRef.current = isRunning;
  }, [isRunning]);
  useEffect(() => {
    disabledRef.current = disabled;
  }, [disabled]);

  // --- Slash command autocomplete ---
  const onCommandRef = useRef(onCommand);
  useEffect(() => { onCommandRef.current = onCommand; }, [onCommand]);
  const [slashCommands, setSlashCommands] = useState<SlashCommand[]>([]);
  const [showSlashMenu, setShowSlashMenu] = useState(false);
  const [slashFilter, setSlashFilter] = useState('');
  const [slashIndex, setSlashIndex] = useState(0);
  const slashMenuRef = useRef<HTMLDivElement>(null);

  // Refs for native listener access
  const showSlashMenuRef = useRef(false);
  const slashIndexRef = useRef(0);
  const filteredCommandsRef = useRef<SlashCommand[]>([]);

  // Fetch commands from OpenCode when directory is available
  useEffect(() => {
    if (!directory) return;
    let cancelled = false;
    api.commands(directory).then(cmds => {
      if (!cancelled) setSlashCommands(cmds || []);
    }).catch(() => { /* no instance running, ignore */ });
    return () => { cancelled = true; };
  }, [directory]);

  const filteredCommands = slashCommands.filter(cmd =>
    cmd.name.toLowerCase().startsWith(slashFilter.toLowerCase())
  );

  // Keep refs in sync with state
  useEffect(() => { showSlashMenuRef.current = showSlashMenu; }, [showSlashMenu]);
  useEffect(() => { slashIndexRef.current = slashIndex; }, [slashIndex]);
  useEffect(() => { filteredCommandsRef.current = filteredCommands; }, [filteredCommands]);

  // Scroll active item into view
  useEffect(() => {
    if (!showSlashMenu || !slashMenuRef.current) return;
    const active = slashMenuRef.current.querySelector('.oc-slash-item.active');
    if (active) active.scrollIntoView({ block: 'nearest' });
  }, [slashIndex, showSlashMenu]);

  // Select a slash command: fill textarea with /<name> and close menu
  const selectSlashCommand = useCallback((cmd: SlashCommand) => {
    const el = inputRef.current;
    if (!el) return;
    el.value = '/' + cmd.name + ' ';
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 200) + 'px';
    el.focus();
    setShowSlashMenu(false);
    setSlashFilter('');
    setSlashIndex(0);
  }, []);

  // Restore draft from localStorage when sessionId changes and focus the input
  useEffect(() => {
    const el = inputRef.current;
    if (!el || !sessionId) return;
    const draft = getDraft(sessionId);
    el.value = draft;
    // Auto-resize the textarea to fit the restored content
    el.style.height = 'auto';
    if (draft) {
      el.style.height = Math.min(el.scrollHeight, 200) + 'px';
    }
    el.focus();
  }, [sessionId]);

  // Refocus the input when clicking anywhere non-interactive on the page
  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!inputRef.current || inputRef.current.disabled) return;
      // Don't steal focus from interactive elements
      if (target.closest('button, a, select, input, textarea, [role="button"], [contenteditable], pre, code, .oc-cmd-palette')) return;
      inputRef.current.focus();
    };
    document.addEventListener('click', handleClick);
    return () => document.removeEventListener('click', handleClick);
  }, []);

  // Flush any pending draft save on unmount
  useEffect(() => {
    const el = inputRef.current;
    return () => {
      if (draftTimerRef.current) {
        clearTimeout(draftTimerRef.current);
        draftTimerRef.current = null;
      }
      // Save current value immediately on unmount
      const sid = sessionIdRef.current;
      if (el && sid) {
        const text = el.value.trim();
        if (text) {
          saveDraft(sid, text);
        } else {
          clearDraft(sid);
        }
      }
    };
  }, []);

  // Helper to add image files
  const addImageFiles = useCallback(async (files: File[]) => {
    const imageFiles = files.filter(f => f.type.startsWith('image/'));
    if (imageFiles.length === 0) return;
    const newImages: AttachedImage[] = [];
    for (const file of imageFiles) {
      try {
        const url = await readFileAsDataURL(file);
        newImages.push({ url, mime: file.type });
      } catch (err) {
        console.error('Failed to read image', err);
      }
    }
    setImages(prev => [...prev, ...newImages]);
  }, []);

  const removeImage = useCallback((index: number) => {
    setImages(prev => prev.filter((_, i) => i !== index));
  }, []);

  // Sync the disabled attribute on the textarea
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.disabled = !!disabled;
  }, [disabled]);

  const setMicState = useCallback((state: 'idle' | 'recording' | 'transcribing') => {
    const btn = micRef.current;
    if (!btn) return;
    const icon = btn.querySelector('.oc-mic-icon');
    if (!(icon instanceof HTMLElement)) return;
    btn.classList.remove('oc-mic-recording', 'oc-mic-transcribing');
    btn.disabled = state === 'transcribing' || !!disabledRef.current;
    icon.className = 'bi oc-mic-icon';
    if (state === 'recording') {
      btn.classList.add('oc-mic-recording');
      icon.classList.add('bi-stop-fill');
    } else if (state === 'transcribing') {
      btn.classList.add('oc-mic-transcribing');
      icon.classList.add('bi-hourglass-split');
    } else {
      icon.classList.add('bi-mic-fill');
    }
  }, []);
  const transcribe = useApiStore((state) => state.transcribe);

  const stopRecording = useCallback((): Blob | null => {
    const ctx = recordingRef.current;
    if (!ctx) return null;
    recordingRef.current = null;

    ctx.processor.disconnect();
    ctx.stream.getTracks().forEach(t => t.stop());

    // Merge all chunks into one Float32Array
    const totalLen = ctx.chunks.reduce((sum, c) => sum + c.length, 0);
    const merged = new Float32Array(totalLen);
    let offset = 0;
    for (const chunk of ctx.chunks) {
      merged.set(chunk, offset);
      offset += chunk.length;
    }

    // Downsample to 16kHz for whisper (from whatever the AudioContext rate is)
    const origRate = ctx.audioCtx.sampleRate;
    ctx.audioCtx.close();

    let samples = merged;
    if (origRate !== 16000) {
      const ratio = origRate / 16000;
      const newLen = Math.floor(merged.length / ratio);
      const downsampled = new Float32Array(newLen);
      for (let i = 0; i < newLen; i++) {
        downsampled[i] = merged[Math.floor(i * ratio)];
      }
      samples = downsampled;
    }

    return encodeWav(samples, 16000);
  }, []);

  const handleMicClick = useCallback(async () => {
    if (disabledRef.current) return;

    // If recording, stop and transcribe
    if (recordingRef.current) {
      setMicState('transcribing');
      const blob = stopRecording();
      if (blob && blob.size > 44) { // > 44 bytes = has actual audio data beyond WAV header
        try {
          const text = await transcribe(blob);
          if (text && inputRef.current) {
            inputRef.current.value += (inputRef.current.value ? ' ' : '') + text;
            inputRef.current.dispatchEvent(new Event('input'));
            inputRef.current.focus();
          }
        } catch (err) {
          console.error('Transcription failed', err);
        }
      }
      setMicState('idle');
      return;
    }

    // Start recording using Web Audio API (works on Safari/iPad)
    try {
      if (!navigator.mediaDevices?.getUserMedia) {
        console.error('getUserMedia not supported');
        return;
      }
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const audioCtx = new (window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext)();
      const source = audioCtx.createMediaStreamSource(stream);
      const processor = audioCtx.createScriptProcessor(4096, 1, 1);
      const chunks: Float32Array[] = [];

      processor.onaudioprocess = (e) => {
        const data = e.inputBuffer.getChannelData(0);
        chunks.push(new Float32Array(data));
      };

      source.connect(processor);
      processor.connect(audioCtx.destination);

      recordingRef.current = { stream, audioCtx, processor, chunks };
      setMicState('recording');
    } catch (err) {
      console.error('Microphone access failed', err);
      setMicState('idle');
    }
  }, [setMicState, stopRecording, transcribe]);

  // Attach native event listeners once, never re-render
  useEffect(() => {
    if (mountedRef.current) return;
    mountedRef.current = true;
    const el = inputRef.current;
    if (!el) return;

    el.addEventListener('keydown', (e) => {
      if (disabledRef.current) return;

      // Slash menu keyboard navigation
      if (showSlashMenuRef.current) {
        const cmds = filteredCommandsRef.current;
        if (e.key === 'ArrowDown') {
          e.preventDefault();
          const next = (slashIndexRef.current + 1) % Math.max(cmds.length, 1);
          slashIndexRef.current = next;
          el.dispatchEvent(new CustomEvent('oc-slash-nav', { detail: next }));
          return;
        }
        if (e.key === 'ArrowUp') {
          e.preventDefault();
          const next = (slashIndexRef.current - 1 + Math.max(cmds.length, 1)) % Math.max(cmds.length, 1);
          slashIndexRef.current = next;
          el.dispatchEvent(new CustomEvent('oc-slash-nav', { detail: next }));
          return;
        }
        if (e.key === 'Escape') {
          e.preventDefault();
          el.dispatchEvent(new CustomEvent('oc-slash-close'));
          return;
        }
        if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
          if (cmds.length > 0) {
            e.preventDefault();
            const cmd = cmds[slashIndexRef.current];
            if (cmd) {
              el.dispatchEvent(new CustomEvent('oc-slash-select', { detail: cmd }));
            }
            return;
          }
        }
      }

      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        const trimmed = el.value.trim();
        const imgs = imagesRef.current;
        if (!trimmed && imgs.length === 0) return;

        // Check if this is a slash command
        if (trimmed.startsWith('/') && onCommandRef.current) {
          const spaceIdx = trimmed.indexOf(' ');
          const command = spaceIdx > 0 ? trimmed.slice(1, spaceIdx) : trimmed.slice(1);
          const args = spaceIdx > 0 ? trimmed.slice(spaceIdx + 1).trim() : '';
          onCommandRef.current(command, args);
        } else {
          onSendRef.current?.(trimmed, imgs.length > 0 ? imgs : undefined);
        }

        el.value = '';
        el.style.height = 'auto';
        // Clear draft from localStorage on send
        const sid = sessionIdRef.current;
        if (sid) {
          if (draftTimerRef.current) clearTimeout(draftTimerRef.current);
          clearDraft(sid);
        }
        // Clear images via a dispatched custom event (since setImages is not in scope here)
        el.dispatchEvent(new CustomEvent('oc-clear-images'));
      }
    });

    el.addEventListener('input', () => {
      el.style.height = 'auto';
      el.style.height = Math.min(el.scrollHeight, 200) + 'px';

      // Slash command detection: show menu when input starts with /
      const val = el.value;
      if (val.startsWith('/') && !val.includes('\n')) {
        const filter = val.slice(1).split(' ')[0] || '';
        el.dispatchEvent(new CustomEvent('oc-slash-update', { detail: { show: true, filter } }));
      } else {
        el.dispatchEvent(new CustomEvent('oc-slash-update', { detail: { show: false, filter: '' } }));
      }
      // Debounced save of draft to localStorage
      const sid = sessionIdRef.current;
      if (sid) {
        if (draftTimerRef.current) clearTimeout(draftTimerRef.current);
        draftTimerRef.current = setTimeout(() => {
          const text = el.value.trim();
          if (text) {
            saveDraft(sid, text);
          } else {
            clearDraft(sid);
          }
        }, 300);
      }
    });

    // Handle paste with images
    el.addEventListener('paste', (e) => {
      if (disabledRef.current) return;
      const items = e.clipboardData?.items;
      if (!items) return;
      const imageFiles: File[] = [];
      for (let i = 0; i < items.length; i++) {
        if (items[i].type.startsWith('image/')) {
          const file = items[i].getAsFile();
          if (file) imageFiles.push(file);
        }
      }
      if (imageFiles.length > 0) {
        e.preventDefault();
        // Dispatch custom event with files to be handled by React
        el.dispatchEvent(new CustomEvent('oc-paste-images', { detail: imageFiles }));
      }
    });

  }, []);

  // ESC key to abort a running session
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      if (e.key === 'Escape' && isRunningRef.current && onAbortRef.current) {
        e.preventDefault();
        onAbortRef.current();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, []);

  // Listen for custom events from native listeners to bridge into React state
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    const handleClearImages = () => setImages([]);
    const handlePasteImages = (e: Event) => {
      const files = (e as CustomEvent).detail as File[];
      addImageFiles(files);
    };
    const handleSlashUpdate = (e: Event) => {
      const { show, filter } = (e as CustomEvent).detail as { show: boolean; filter: string };
      setShowSlashMenu(show);
      setSlashFilter(filter);
      if (!show) setSlashIndex(0);
    };
    const handleSlashNav = (e: Event) => {
      setSlashIndex((e as CustomEvent).detail as number);
    };
    const handleSlashClose = () => {
      setShowSlashMenu(false);
      setSlashFilter('');
      setSlashIndex(0);
    };
    const handleSlashSelect = (e: Event) => {
      selectSlashCommand((e as CustomEvent).detail as SlashCommand);
    };
    el.addEventListener('oc-clear-images', handleClearImages);
    el.addEventListener('oc-paste-images', handlePasteImages);
    el.addEventListener('oc-slash-update', handleSlashUpdate);
    el.addEventListener('oc-slash-nav', handleSlashNav);
    el.addEventListener('oc-slash-close', handleSlashClose);
    el.addEventListener('oc-slash-select', handleSlashSelect);
    return () => {
      el.removeEventListener('oc-clear-images', handleClearImages);
      el.removeEventListener('oc-paste-images', handlePasteImages);
      el.removeEventListener('oc-slash-update', handleSlashUpdate);
      el.removeEventListener('oc-slash-nav', handleSlashNav);
      el.removeEventListener('oc-slash-close', handleSlashClose);
      el.removeEventListener('oc-slash-select', handleSlashSelect);
    };
  }, [addImageFiles, selectSlashCommand]);

  // Handle drag and drop on the composer wrapper
  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  }, []);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (disabled) return;
    const files = Array.from(e.dataTransfer.files);
    addImageFiles(files);
  }, [disabled, addImageFiles]);

  // Build agent options: active agent + known agents, deduplicated
  const agentOptions = Array.from(new Set(
    [activeAgent, ...KNOWN_AGENTS].filter((a): a is string => !!a)
  ));
  const effectiveAgent = selectedAgent || activeAgent || '';
  const effectiveModel = selectedModel || activeModel || '';
  const modelGroups = (models || []).reduce<Array<{ label: string; options: { value: string; label: string }[] }>>((groups, model) => {
    const slashIndex = model.indexOf('/');
    const provider = slashIndex > 0 ? model.slice(0, slashIndex) : 'Other';
    const optionLabel = slashIndex > 0 ? model.slice(slashIndex + 1) : model;
    const existing = groups.find((group) => group.label === provider);

    if (existing) {
      existing.options.push({ value: model, label: optionLabel });
      return groups;
    }

    groups.push({
      label: provider,
      options: [{ value: model, label: optionLabel }],
    });
    return groups;
  }, []);

  return (
    <div
      className={`oc-composer-wrap${disabled ? ' oc-composer-disabled' : ''}`}
      ref={wrapRef}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {showSlashMenu && filteredCommands.length > 0 && (
        <div className="oc-slash-menu" ref={slashMenuRef}>
          {filteredCommands.map((cmd, i) => (
            <div
              key={cmd.name}
              className={`oc-slash-item${i === slashIndex ? ' active' : ''}`}
              onMouseDown={(e) => { e.preventDefault(); selectSlashCommand(cmd); }}
              onMouseEnter={() => setSlashIndex(i)}
            >
              <span className="oc-slash-name">/{cmd.name}</span>
              {cmd.description && <span className="oc-slash-desc">{cmd.description}</span>}
            </div>
          ))}
        </div>
      )}
      <div className="oc-composer">
        {images.length > 0 && (
          <div className="oc-composer-images">
            {images.map((img, i) => (
              <div key={i} className="oc-composer-image-thumb">
                <img src={img.url} alt={`Attachment ${i + 1}`} />
                <button className="oc-composer-image-remove" onClick={() => removeImage(i)}>{'\u00D7'}</button>
              </div>
            ))}
          </div>
        )}
        <textarea
          ref={inputRef}
          className="oc-composer-input"
          rows={1}
          disabled={disabled}
          placeholder={disabled ? 'No running OpenCode instance' : undefined}
        />
        <div className="oc-composer-bar">
          <div className="oc-composer-bar-left">
            <select
              className="oc-bar-select"
              disabled={disabled}
              value={effectiveAgent}
              onChange={(e) => onAgentChange?.(e.target.value)}
              title="Agent"
            >
              {agentOptions.map((agent) => (
                <option key={agent} value={agent}>{agent}</option>
              ))}
            </select>
            {models && models.length > 0 && (
              <select
                className="oc-bar-select"
                disabled={disabled}
                value={models.includes(effectiveModel) ? effectiveModel : ''}
                onChange={(e) => onModelChange?.(e.target.value)}
                title="Model"
              >
                {modelGroups.map((group) => (
                  <optgroup key={group.label} label={group.label}>
                    {group.options.map((model) => (
                      <option key={model.value} value={model.value}>{model.label}</option>
                    ))}
                  </optgroup>
                ))}
              </select>
            )}
            {disabled && <span className="oc-bar-hint">No running OpenCode instance</span>}
          </div>
          <div className="oc-composer-bar-right">
            <button
              className="oc-bar-action"
              onClick={() => fileInputRef.current?.click()}
              disabled={disabled}
              title="Attach image"
            >+</button>
            <input
              ref={fileInputRef}
              type="file"
              accept="image/*"
              multiple
              style={{ display: 'none' }}
              onChange={(e) => {
                const files = Array.from(e.target.files || []);
                addImageFiles(files);
                e.target.value = '';
              }}
            />
            {whisperAvailable && (
              <button
                ref={micRef}
                className="oc-bar-action"
                onClick={handleMicClick}
                disabled={disabled}
                title="Record voice message"
              >
                <i className="bi bi-mic-fill oc-mic-icon" aria-hidden="true" />
              </button>
            )}
          </div>
        </div>
      </div>
      <div className="oc-composer-footer">
        <span className="oc-composer-footer-left">
          {!disabled && isRunning && (
            <>
              <span className="oc-bar-dots">
                <span className="oc-thinking-dot" /><span className="oc-thinking-dot" /><span className="oc-thinking-dot" />
              </span>
              <button
                type="button"
                className="oc-stop-btn"
                onClick={() => onAbort?.()}
                title="Stop generation (Esc)"
              >
                <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
                  <rect x="1" y="1" width="8" height="8" rx="1.5" fill="currentColor" />
                </svg>
              </button>
              <span className="oc-stop-hint">Esc to interrupt</span>
            </>
          )}
        </span>
        <span className="oc-composer-footer-right">
          {contextTokens != null && contextTokens > 0 && (() => {
            const contextWindow = getContextWindow(activeModel);
            const pct = contextWindow ? Math.min(100, (contextTokens / contextWindow) * 100) : null;
            return (
              <span className={`oc-context-usage${pct != null && pct > 80 ? ' oc-context-warn' : ''}`} title={contextWindow ? `${contextTokens.toLocaleString()} / ${contextWindow.toLocaleString()} tokens` : `${contextTokens.toLocaleString()} tokens used`}>
                {formatTokenCount(contextTokens)}{pct != null && ` (${pct.toFixed(0)}%)`}
              </span>
            );
          })()}
          <span className="oc-keybind-hint">? for shortcuts</span>
        </span>
      </div>
    </div>
  );
}

// Re-render when status, model, agent, context tokens, session, or mic availability changes.
// Other props (onSend) are accessed via refs and don't need re-renders.
const MemoComposer = memo(Composer, (prev, next) =>
  prev.isRunning === next.isRunning &&
  prev.disabled === next.disabled &&
  prev.whisperAvailable === next.whisperAvailable &&
  prev.activeModel === next.activeModel &&
  prev.selectedModel === next.selectedModel &&
  prev.activeAgent === next.activeAgent &&
  prev.selectedAgent === next.selectedAgent &&
  prev.contextTokens === next.contextTokens &&
  prev.sessionId === next.sessionId &&
  prev.directory === next.directory &&
  (prev.models?.length || 0) === (next.models?.length || 0) &&
  (prev.models || []).every((model, i) => model === (next.models || [])[i])
);
// Note: onSend, onAbort, onCommand are accessed via refs, so they don't need re-renders.
export { MemoComposer as Composer };

export function AssistantThread({ hasMore, loadingMore, onLoadMore, composer, footer }: { hasMore?: boolean; loadingMore?: boolean; onLoadMore?: () => void; composer?: React.ReactNode; footer?: React.ReactNode }) {
  const threadRef = useRef<HTMLDivElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const [showScrollBtn, setShowScrollBtn] = useState(false);
  const [bottomInset, setBottomInset] = useState(140);
  const wasAtBottomRef = useRef(true);
  const hasMoreRef = useRef(hasMore);
  const loadingMoreRef = useRef(loadingMore);
  const onLoadMoreRef = useRef(onLoadMore);
  useEffect(() => { hasMoreRef.current = hasMore; }, [hasMore]);
  useEffect(() => { loadingMoreRef.current = loadingMore; }, [loadingMore]);
  useEffect(() => { onLoadMoreRef.current = onLoadMore; }, [onLoadMore]);

  const isAtBottom = useCallback(() => {
    const el = viewportRef.current;
    if (!el) return true;
    return el.scrollHeight - el.clientHeight - el.scrollTop < 100;
  }, []);

  const checkScroll = useCallback(() => {
    const atBottom = isAtBottom();
    wasAtBottomRef.current = atBottom;
    setShowScrollBtn(!atBottom);

    // Auto-load older messages when scrolled near the top
    const el = viewportRef.current;
    if (el && el.scrollTop < 200 && hasMoreRef.current && !loadingMoreRef.current) {
      onLoadMoreRef.current?.();
    }
  }, [isAtBottom]);

  useEffect(() => {
    const thread = threadRef.current;
    if (!thread) return;

    const resizeObserver = new ResizeObserver(() => {
      const overlay = thread.querySelector<HTMLElement>('.oc-composer-wrap, .oc-permission-wrap');
      setBottomInset((overlay?.offsetHeight || 124) + 16);
    });
    const mutationObserver = new MutationObserver(() => {
      const overlay = thread.querySelector<HTMLElement>('.oc-composer-wrap, .oc-permission-wrap');
      resizeObserver.disconnect();
      if (overlay) resizeObserver.observe(overlay);
      setBottomInset((overlay?.offsetHeight || 124) + 16);
    });

    const overlay = thread.querySelector<HTMLElement>('.oc-composer-wrap, .oc-permission-wrap');
    if (overlay) resizeObserver.observe(overlay);
    setBottomInset((overlay?.offsetHeight || 124) + 16);

    mutationObserver.observe(thread, { childList: true, subtree: true });
    return () => {
      mutationObserver.disconnect();
      resizeObserver.disconnect();
    };
  }, [composer]);

  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    el.addEventListener('scroll', checkScroll, { passive: true });

    // Auto-scroll when content changes (messages added, tool calls updated, etc.)
    const observer = new MutationObserver(() => {
      if (wasAtBottomRef.current) {
        el.scrollTop = el.scrollHeight;
      }
      checkScroll();
    });
    observer.observe(el, { childList: true, subtree: true, characterData: true });

    const frame = requestAnimationFrame(checkScroll);
    return () => {
      cancelAnimationFrame(frame);
      el.removeEventListener('scroll', checkScroll);
      observer.disconnect();
    };
  }, [checkScroll]);

  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;

    requestAnimationFrame(() => {
      if (wasAtBottomRef.current) {
        el.scrollTop = el.scrollHeight;
      }
      checkScroll();
    });
  }, [bottomInset, checkScroll]);

  // Auto-scroll to bottom on initial load
  useEffect(() => {
    const el = viewportRef.current;
    if (el) {
      const timer = setTimeout(() => {
        el.scrollTop = el.scrollHeight;
        checkScroll();
      }, 100);
      return () => clearTimeout(timer);
    }
  }, [checkScroll]);

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

  const scrollToBottom = () => {
    const el = viewportRef.current;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
  };

  useEffect(() => {
    const jumpToUserMessage = (direction: 'next' | 'prev') => {
      const viewport = viewportRef.current;
      if (!viewport) return;

      const userMessages = Array.from(viewport.querySelectorAll<HTMLElement>('.oc-msg-user'));
      if (userMessages.length === 0) return;

      const viewportRect = viewport.getBoundingClientRect();
      const viewportMiddle = viewportRect.top + viewportRect.height / 2;
      const positions = userMessages.map((message) => {
        const rect = message.getBoundingClientRect();
        return {
          message,
          middle: rect.top + rect.height / 2,
        };
      });

      const currentIndex = positions.reduce((bestIndex, entry, index, arr) => {
        if (bestIndex === -1) return index;
        const bestDistance = Math.abs(arr[bestIndex].middle - viewportMiddle);
        const currentDistance = Math.abs(entry.middle - viewportMiddle);
        return currentDistance < bestDistance ? index : bestIndex;
      }, -1);

      const currentEntry = positions[currentIndex];
      const target = direction === 'next'
        ? positions.find((entry) => entry.middle > currentEntry.middle + 4)?.message
        : [...positions].reverse().find((entry) => entry.middle < currentEntry.middle - 4)?.message;

      if (!target && direction === 'next') {
        viewport.scrollTo({ top: viewport.scrollHeight, behavior: 'smooth' });
        return;
      }

      (target || currentEntry.message).scrollIntoView({ block: 'start', behavior: 'smooth' });
    };

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.defaultPrevented || !e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
      if (e.key !== 'j' && e.key !== 'k') return;

      e.preventDefault();
      jumpToUserMessage(e.key === 'j' ? 'next' : 'prev');
    };

    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, []);

  return (
    <div ref={threadRef} style={{ display: 'flex', flex: 1, minHeight: 0 }}>
      <ThreadPrimitive.Root className="oc-thread">
        <div ref={viewportRef} className="oc-thread-viewport" style={{ paddingBottom: bottomInset }}>
          {hasMore && (
            <div className="oc-load-more">
              <button onClick={onLoadMore} disabled={loadingMore}>
                {loadingMore ? 'Loading...' : 'Load older messages'}
              </button>
            </div>
          )}
          <ThreadPrimitive.Empty>
            <div className="oc-empty">No messages yet.</div>
          </ThreadPrimitive.Empty>
          <ThreadPrimitive.Messages
            components={{ UserMessage, AssistantMessage }}
          />
        </div>
        {showScrollBtn && (
          <button className="oc-scroll-btn" onClick={scrollToBottom}>
            Scroll to bottom
          </button>
        )}
        {footer && <div className="oc-thread-overlay">{footer}</div>}
        {composer}
      </ThreadPrimitive.Root>
    </div>
  );
}
