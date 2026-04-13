import { useState, useEffect, useRef, useCallback, memo } from 'react';
import {
  ThreadPrimitive,
  MessagePrimitive,
  useMessage,
  type ToolCallMessagePartProps,
} from '@assistant-ui/react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { FC } from 'react';

const MarkdownText: FC<{ text: string }> = ({ text }) => {
  if (!text.trim()) return null;
  return <ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown>;
};

const UserMessage: FC = () => {
  const content = useMessage((m) => m.content);
  const hasContent = content.some(
    (p) => (p.type === 'text' && 'text' in p && (p as { text: string }).text.trim()) || p.type === 'tool-call'
  );
  if (!hasContent) return null;

  return (
    <MessagePrimitive.Root className="oc-msg oc-msg-user">
      <div className="oc-msg-body">
        <MessagePrimitive.Content
          components={{ Text: ({ text }) => text.trim() ? <span style={{ whiteSpace: 'pre-wrap' }}>{text}</span> : null }}
        />
      </div>
    </MessagePrimitive.Root>
  );
};

const AssistantMessage: FC = () => {
  const content = useMessage((m) => m.content);
  const hasContent = content.some(
    (p) => (p.type === 'text' && 'text' in p && (p as { text: string }).text.trim()) || p.type === 'tool-call'
  );
  if (!hasContent) return null;

  return (
    <MessagePrimitive.Root className="oc-msg oc-msg-assistant">
      <div className="oc-msg-body oc-md">
        <MessagePrimitive.Content
          components={{
            Text: MarkdownText,
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
  if (!createdAt || createdAt.getTime() === 0) return null;
  if (status?.type === 'running') return null;
  const time = createdAt.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
  return (
    <div className="oc-msg-meta">
      <span className="oc-meta-dot" />
      <span>{time}</span>
    </div>
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
            <span className="oc-todo-check">{isDone ? '\u2713' : isActive ? '\u25B6' : '\u25CB'}</span>
            <span className="oc-todo-text">{t.content}</span>
            {t.priority === 'high' && <span className="oc-todo-priority">!</span>}
          </div>
        );
      })}
    </div>
  );
}

const ToolCallDisplay: FC<ToolCallMessagePartProps> = ({ toolName, argsText, result }) => {
  const [expanded, setExpanded] = useState(false);

  // First line of argsText is the tool's own status (completed/running/error)
  const lines = (argsText || '').split('\n');
  const toolStatus = lines[0] || 'running';
  const remainingArgs = lines.slice(1).join('\n');

  // Hide empty/loading tool calls
  const hasArgs = remainingArgs.trim() && remainingArgs.trim() !== '{}';
  const hasResult = result && String(result).trim() && String(result).trim() !== '{}';
  if (!hasArgs && !hasResult && toolStatus !== 'completed') return null;

  let statusIcon = '\u2022';
  let statusClass = 'oc-tool-running';
  if (toolStatus === 'completed') { statusIcon = '\u2713'; statusClass = 'oc-tool-done'; }
  else if (toolStatus === 'error') { statusIcon = '\u2717'; statusClass = 'oc-tool-error'; }

  let outputDisplay = '';
  if (typeof result === 'string') outputDisplay = result;
  else if (result != null) outputDisplay = JSON.stringify(result, null, 2);

  const argLines = remainingArgs.split('\n');
  const title = argLines[0] || toolName;
  const detail = argLines.slice(1).join('\n').trim();

  const isLong = outputDisplay.length > 500 || (detail && detail.length > 300);

  // Detect TodoWrite tool calls and render as a checklist
  const isTodo = toolName === 'mcp_todowrite' || toolName === 'todowrite' || toolName === 'TodoWrite';
  const todos = isTodo ? parseTodos(detail, result) : null;

  if (todos) {
    return (
      <div className={`oc-tool ${statusClass}`}>
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
          <span className={`oc-tool-icon ${statusClass}`}>{statusIcon}</span>
          <span className="oc-tool-label">{title && title !== toolName ? title : 'Task list'}</span>
        </div>
        <div className="oc-tool-content">
          <TodoList todos={todos} />
        </div>
      </div>
    );
  }

  return (
    <div className={`oc-tool ${statusClass} ${expanded ? 'oc-tool-expanded' : ''}`}>
      <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
        <span className={`oc-tool-icon ${statusClass}`}>{statusIcon}</span>
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

function Composer({ onSend, isRunning }: { onSend?: (text: string) => void; isRunning: boolean }) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const onSendRef = useRef(onSend);
  const isRunningRef = useRef(isRunning);
  const mountedRef = useRef(false);

  // Keep refs in sync without re-rendering
  onSendRef.current = onSend;
  isRunningRef.current = isRunning;

  // Update status bar text via DOM, not React state
  useEffect(() => {
    const bar = wrapRef.current?.querySelector('.oc-composer-bar-left');
    if (!bar) return;
    if (isRunning) {
      bar.innerHTML = '<span class="oc-bar-dots"><span class="oc-thinking-dot"></span><span class="oc-thinking-dot"></span><span class="oc-thinking-dot"></span></span><span class="oc-bar-hint">esc interrupt</span>';
    } else {
      bar.innerHTML = '<span class="oc-bar-hint">enter send</span>';
    }
  }, [isRunning]);

  // Attach native event listeners once, never re-render
  useEffect(() => {
    if (mountedRef.current) return;
    mountedRef.current = true;
    const el = inputRef.current;
    if (!el) return;

    el.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        const trimmed = el.value.trim();
        if (!trimmed) return;
        onSendRef.current?.(trimmed);
        el.value = '';
        el.style.height = 'auto';
      }
    });

    el.addEventListener('input', () => {
      el.style.height = 'auto';
      el.style.height = Math.min(el.scrollHeight, 200) + 'px';
    });

  }, []);

  // Only render the shell once — never re-renders
  return (
    <div className="oc-composer-wrap" ref={wrapRef}>
      <div className="oc-composer">
        <textarea
          ref={inputRef}
          className="oc-composer-input"
          rows={1}
        />
      </div>
      <div className="oc-composer-bar">
        <div className="oc-composer-bar-left">
          <span className="oc-bar-hint">enter send</span>
        </div>
      </div>
    </div>
  );
}

const MemoComposer = memo(Composer, () => true);
export { MemoComposer as Composer };

export function AssistantThread({ hasMore, loadingMore, onLoadMore, composer, footer }: { hasMore?: boolean; loadingMore?: boolean; onLoadMore?: () => void; composer?: React.ReactNode; footer?: React.ReactNode }) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const [showScrollBtn, setShowScrollBtn] = useState(false);
  const wasAtBottomRef = useRef(true);
  const hasMoreRef = useRef(hasMore);
  const loadingMoreRef = useRef(loadingMore);
  const onLoadMoreRef = useRef(onLoadMore);
  hasMoreRef.current = hasMore;
  loadingMoreRef.current = loadingMore;
  onLoadMoreRef.current = onLoadMore;

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

    checkScroll();
    return () => {
      el.removeEventListener('scroll', checkScroll);
      observer.disconnect();
    };
  }, [checkScroll]);

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

  return (
    <ThreadPrimitive.Root className="oc-thread">
      <div ref={viewportRef} className="oc-thread-viewport">
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
      {composer}
      {footer}
    </ThreadPrimitive.Root>
  );
}
