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
  const hasContent = content.some(
    (p) => (p.type === 'text' && 'text' in p && (p as { text: string }).text.trim()) || p.type === 'tool-call' || p.type === 'image'
  );
  if (!hasContent) return null;

  return (
    <MessagePrimitive.Root className="oc-msg oc-msg-user">
      <div className="oc-msg-body">
        <MessagePrimitive.Content
          components={{
            Text: ({ text }) => text.trim() ? <span style={{ whiteSpace: 'pre-wrap' }}>{text}</span> : null,
            Image: ImageDisplay,
          }}
        />
      </div>
    </MessagePrimitive.Root>
  );
};

const AssistantMessage: FC = () => {
  const content = useMessage((m) => m.content);
  const hasContent = content.some(
    (p) => (p.type === 'text' && 'text' in p && (p as { text: string }).text.trim()) || p.type === 'tool-call' || p.type === 'image'
  );
  if (!hasContent) return null;

  // Messages that only contain muted tool calls (reads/greps) render as a compact list
  const onlyMuted = content.every(
    (p) => p.type === 'tool-call' && 'toolName' in p && (p as { toolName: string }).toolName === '__read__'
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
    (p) => p.type === 'tool-call' && 'toolName' in p && (p as { toolName: string }).toolName === '__read__'
  );
  if (onlyReads) return null;
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
  // File reads/greps render as a muted inline line with an arrow icon
  if (toolName === '__read__') {
    return (
      <div className="oc-read-line">
        <span className="oc-read-arrow">{'\u2192'}</span>
        <span>{argsText || 'Read'}</span>
      </div>
    );
  }

  // Subagent tasks render as a compact card with output, clicking header opens the session
  if (toolName === '__task__') {
    const [taskExpanded, setTaskExpanded] = useState(false);
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
    if (taskStatus === 'completed') { statusIcon = '\u2713'; statusClass = 'oc-tool-done'; }
    else if (taskStatus === 'error') { statusIcon = '\u2717'; statusClass = 'oc-tool-error'; }

    const handleHeaderClick = sessionId ? () => { window.location.href = `/session/${sessionId}`; } : undefined;
    const isLongOutput = taskOutput.length > 500;

    return (
      <div className={`oc-tool oc-tool-task ${statusClass} ${taskExpanded ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header" onClick={handleHeaderClick} style={sessionId ? { cursor: 'pointer' } : undefined}>
          <span className={`oc-tool-icon ${statusClass}`}>{statusIcon}</span>
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

  const [expanded, setExpanded] = useState(false);

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

  // Shell commands get a terminal-style rendering
  const isBash = toolName === 'bash' || toolName === 'mcp_bash';
  if (isBash) {
    const command = detail || title;
    return (
      <div className={`oc-tool oc-tool-shell ${statusClass} ${expanded ? 'oc-tool-expanded' : ''}`}>
        <div className="oc-tool-header" onClick={() => setExpanded(!expanded)}>
          <span className={`oc-tool-icon ${statusClass}`}>{statusIcon}</span>
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

function Composer({ onSend, isRunning, disabled, whisperAvailable }: { onSend?: (text: string) => void; isRunning: boolean; disabled?: boolean; whisperAvailable?: boolean }) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const micRef = useRef<HTMLButtonElement>(null);
  const onSendRef = useRef(onSend);
  const isRunningRef = useRef(isRunning);
  const disabledRef = useRef(disabled);
  const mountedRef = useRef(false);
  const recordingRef = useRef<RecordingCtx | null>(null);

  // Keep refs in sync via effect to satisfy lint rules
  useEffect(() => {
    onSendRef.current = onSend;
  }, [onSend]);
  useEffect(() => {
    isRunningRef.current = isRunning;
  }, [isRunning]);
  useEffect(() => {
    disabledRef.current = disabled;
  }, [disabled]);

  // Update status bar text via DOM, not React state
  useEffect(() => {
    const bar = wrapRef.current?.querySelector('.oc-composer-bar-left');
    if (!bar) return;
    if (disabled) {
      bar.innerHTML = '<span class="oc-bar-hint">No running OpenCode instance</span>';
    } else if (isRunning) {
      bar.innerHTML = '<span class="oc-bar-dots"><span class="oc-thinking-dot"></span><span class="oc-thinking-dot"></span><span class="oc-thinking-dot"></span></span><span class="oc-bar-hint">esc interrupt</span>';
    } else {
      bar.innerHTML = '<span class="oc-bar-hint">enter send</span>';
    }
  }, [isRunning, disabled]);

  // Sync the disabled attribute on the textarea
  useEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.disabled = !!disabled;
  }, [disabled]);

  const setMicState = useCallback((state: 'idle' | 'recording' | 'transcribing') => {
    const btn = micRef.current;
    if (!btn) return;
    btn.classList.remove('oc-mic-recording', 'oc-mic-transcribing');
    btn.disabled = state === 'transcribing' || !!disabledRef.current;
    if (state === 'recording') {
      btn.classList.add('oc-mic-recording');
      btn.textContent = '\u25A0'; // stop square
    } else if (state === 'transcribing') {
      btn.classList.add('oc-mic-transcribing');
      btn.textContent = '\u2026'; // ellipsis
    } else {
      btn.textContent = '\u2399';
    }
  }, []);

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
          const { api } = await import('../lib/api');
          const text = await api.transcribe(blob);
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
  }, [setMicState, stopRecording]);

  // Attach native event listeners once, never re-render
  useEffect(() => {
    if (mountedRef.current) return;
    mountedRef.current = true;
    const el = inputRef.current;
    if (!el) return;

    el.addEventListener('keydown', (e) => {
      if (disabledRef.current) return;
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
    <div className={`oc-composer-wrap${disabled ? ' oc-composer-disabled' : ''}`} ref={wrapRef}>
      <div className="oc-composer">
        <textarea
          ref={inputRef}
          className="oc-composer-input"
          rows={1}
          disabled={disabled}
          placeholder={disabled ? 'No running OpenCode instance' : undefined}
        />
        {whisperAvailable && (
          <button
            ref={micRef}
            className="oc-mic-btn"
            onClick={handleMicClick}
            disabled={disabled}
            title="Record voice message"
          >{'\u2399'}</button>
        )}
      </div>
      <div className="oc-composer-bar">
        <div className="oc-composer-bar-left">
          <span className="oc-bar-hint">{disabled ? 'No running OpenCode instance' : 'enter send'}</span>
        </div>
      </div>
    </div>
  );
}

// Re-render when isRunning, disabled, or whisperAvailable changes.
// Other props (onSend) are accessed via refs and don't need re-renders.
const MemoComposer = memo(Composer, (prev, next) =>
  prev.isRunning === next.isRunning &&
  prev.disabled === next.disabled &&
  prev.whisperAvailable === next.whisperAvailable
);
export { MemoComposer as Composer };

export function AssistantThread({ hasMore, loadingMore, onLoadMore, composer, footer }: { hasMore?: boolean; loadingMore?: boolean; onLoadMore?: () => void; composer?: React.ReactNode; footer?: React.ReactNode }) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const [showScrollBtn, setShowScrollBtn] = useState(false);
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
