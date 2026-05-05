import type { ThreadMessageLike } from '@assistant-ui/react';
import type { Message, Part, PartData, FilePart } from './api';
import type { FailedSend } from './failedSends';
import { simpleDiff } from './diff';
import { extractTaskId } from './taskId';

/**
 * Returns true when the MIME type denotes an image (`image/...`).
 * Used to decide whether a `file` part should render inline as an
 * image or as a downloadable attachment label.
 */
export function isImageMime(mime: string | undefined): boolean {
  return !!mime && mime.startsWith('image/');
}

/**
 * WeakMap cache for parsed part data. Keyed on the Part object
 * identity — when a part is updated immutably (new reference) the
 * old entry is automatically garbage-collected. Avoids re-running
 * `JSON.parse` on every SSE delta for parts that haven't changed.
 */
const parsedPartCache = new WeakMap<Part, PartData>();

/**
 * Parse a `Part`'s `data` field into a typed `PartData`. The result
 * is cached per Part instance, so identical part references skip
 * parsing on subsequent calls.
 */
export function parsePart(p: Part): PartData {
  const cached = parsedPartCache.get(p);
  if (cached !== undefined) return cached;
  let result: PartData;
  try {
    result = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
  } catch {
    result = (p.data || {}) as PartData;
  }
  parsedPartCache.set(p, result);
  return result;
}

/**
 * Truncate a string to at most `max` characters, appending a marker
 * with the original length so the reader knows truncation happened.
 * Returns the empty string for null/undefined/empty input.
 */
export function truncate(text: string | undefined | null, max: number): string {
  if (!text) return '';
  if (text.length <= max) return text;
  return text.slice(0, max) + '\n... (' + text.length + ' chars total)';
}

/**
 * Compute a path relative to the session's project directory.
 *
 * When the file lives under `projectDir`, returns the path with the
 * project prefix stripped (so reads display as `internal/db/foo.go`
 * instead of just `foo.go`). For files outside the project, returns
 * the full path so the reader can tell that the file lives outside
 * the checkout (e.g. `/etc/hosts`, `~/.config/foo`).
 */
export function relativizePath(absPath: string, projectDir: string): string {
  if (!absPath) return absPath;
  if (projectDir) {
    // Normalize the project directory by stripping a single trailing
    // slash, then check if absPath sits under it. Use `${dir}/` for
    // the prefix check so `/foo/bar` doesn't accidentally match
    // `/foo/barn`.
    const dir = projectDir.replace(/\/+$/, '');
    if (absPath === dir) return '.';
    const prefix = dir + '/';
    if (absPath.startsWith(prefix)) return absPath.slice(prefix.length);
  }
  return absPath;
}

/**
 * Determine if the session is actively running based on the last
 * message. The assistant is running if the last message has no
 * finish reason (still streaming). Any finish value ("stop",
 * "tool-calls", etc.) means that turn is done. A message with an
 * error object is also not running.
 */
export function computeIsRunning(messages: Message[]): boolean {
  if (messages.length === 0) return false;
  const last = messages[messages.length - 1];
  if (!last.data) return false;
  if (last.data.role === 'user') return true;
  if (last.data.role === 'assistant' && !last.data.finish && !last.data.error) return true;
  return false;
}

/**
 * Per-message conversion cache. Stores the last conversion result
 * for each message, keyed on the message reference. The cache entry
 * also records the parts array reference and context values that
 * were used, so we can detect when a recomputation is needed.
 *
 * This avoids re-converting the entire thread on every SSE delta —
 * only the message whose parts changed gets recomputed.
 */
type ConvertedCacheEntry = {
  parts: Part[];
  pendingAgent: string | undefined;
  taskLiveOutput: Record<string, string> | undefined;
  projectDirectory: string | undefined;
  failedById: Record<string, FailedSend> | undefined;
  /** Whether this message was queued (depends on neighbors). */
  isQueued: boolean;
  /** Resolved agent for this message (depends on neighbors). */
  msgAgent: string | undefined;
  result: ThreadMessageLike;
};
const convertedMessageCache = new WeakMap<Message, ConvertedCacheEntry>();

/**
 * Module-level cache of the last convertMessages result array. When
 * every element in the new result is the same reference as the
 * previous one (all per-message caches hit), we return the old array
 * to preserve referential equality for useSyncExternalStore.
 */
let lastConvertedResult: ThreadMessageLike[] | null = null;

/** Stable empty array for messages with no parts. */
const EMPTY_PARTS: Part[] = [];

/**
 * Shallow element-wise equality for Part arrays. Returns true when both
 * arrays have the same length and every element is the same reference.
 * This is needed because `partsByMsg` builds a fresh array on every
 * `convertMessages` call, so reference equality (`===`) always fails
 * even when the underlying Part objects haven't changed.
 */
function partsEqual(a: Part[], b: Part[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

/**
 * Convert ocman's `Message` + `Part` arrays into the
 * `ThreadMessageLike[]` shape that assistant-ui's external-store
 * runtime expects. Pure function, with a per-message WeakMap cache
 * keyed on input identities so streaming deltas only re-render the
 * one message that actually changed.
 *
 * Behaviour notes:
 *   - Filters to user/assistant messages only (system / tool roles
 *     are dropped).
 *   - Detects "queued" user messages — a user message that follows
 *     an unfinished assistant turn after the session already had a
 *     prior user turn — and surfaces them via `metadata.custom.queued`.
 *   - Resolves the responsible agent for each message and surfaces
 *     it via `metadata.custom.agent` so the renderer can colour
 *     bubbles consistently.
 *   - Special-cases tool calls (read/grep/glob/webfetch/edit/write/
 *     skill/task/question) into their compact rendering forms.
 */
export function convertMessages(
  messages: Message[],
  parts: Part[],
  pendingAgent?: string,
  taskLiveOutput?: Record<string, string>,
  projectDirectory?: string,
  failedById?: Record<string, FailedSend>,
): ThreadMessageLike[] {
  const partsByMsg: Record<string, Part[]> = {};
  parts.forEach((p) => {
    if (!partsByMsg[p.messageId]) partsByMsg[p.messageId] = [];
    partsByMsg[p.messageId].push(p);
  });

  const filtered = messages.filter((m) => m.data?.role === 'user' || m.data?.role === 'assistant');
  const result = filtered.map((m, idx): ThreadMessageLike => {
    const role = m.data.role as 'user' | 'assistant';

    // A user message is "queued" when it follows an unfinished
    // assistant turn and the session already had a prior user turn.
    // New UI-created sessions can start with an assistant bootstrap
    // message, which should not make the first user message look
    // queued.
    let isQueued = false;
    if (role === 'user' && idx > 0) {
      const prev = filtered[idx - 1];
      const hasPriorUserTurn = filtered.slice(0, idx - 1).some((entry) => entry.data?.role === 'user');
      if (
        hasPriorUserTurn &&
        prev.data?.role === 'assistant' &&
        !prev.data.finish &&
        !prev.data.error
      ) {
        isQueued = true;
      }
    }

    // Resolve the agent associated with this message so the UI can
    // colour it. For assistant messages the agent is on the message
    // itself. For user messages we attribute the colour to the agent
    // that replies — i.e. the next assistant message in the thread —
    // falling back to the currently-selected agent when the reply
    // hasn't been produced yet.
    let msgAgent: string | undefined;
    if (role === 'assistant') {
      msgAgent = m.data.agent || undefined;
    } else if (role === 'user') {
      for (let j = idx + 1; j < filtered.length; j++) {
        const later = filtered[j];
        if (later.data?.role === 'assistant' && later.data.agent) {
          msgAgent = later.data.agent;
          break;
        }
      }
      if (!msgAgent) msgAgent = pendingAgent || undefined;
    }

    // Per-message cache check: reuse the previous conversion result
    // when the message reference, its parts, and all context values
    // are unchanged. Parts are compared element-wise (same length +
    // same Part references) because `partsByMsg` builds a fresh array
    // on every call even when the underlying Part objects are stable.
    const msgPartsRaw = partsByMsg[m.id] || EMPTY_PARTS;
    const cached = convertedMessageCache.get(m);
    if (
      cached &&
      partsEqual(cached.parts, msgPartsRaw) &&
      cached.pendingAgent === pendingAgent &&
      cached.taskLiveOutput === taskLiveOutput &&
      cached.projectDirectory === projectDirectory &&
      cached.failedById === failedById &&
      cached.isQueued === isQueued &&
      cached.msgAgent === msgAgent
    ) {
      return cached.result;
    }

    const msgParts = msgPartsRaw.map(parsePart);

    // Build content as string | content array. Using string for
    // simple text, and the full content array format for messages
    // with tool calls or images.
    const textPieces: string[] = [];
    const imageParts: Array<{ type: 'image'; image: string }> = [];
    const toolCalls: Array<{
      type: 'tool-call';
      toolCallId: string;
      toolName: string;
      argsText: string;
      result?: string;
    }> = [];

    msgParts.forEach((pd) => {
      // Skip non-renderable lifecycle parts
      if (pd.type === 'step-start' || pd.type === 'step-finish' || pd.type === 'snapshot') return;

      switch (pd.type) {
        case 'text':
          if (pd.text?.trim()) {
            textPieces.push(pd.text);
          }
          break;
        case 'tool': {
          const st = pd.state || {};
          const input = st.input || {};
          const inp = input as Record<string, string>;
          let argsText = '';
          if (typeof input === 'string') argsText = input;
          else if (inp.command) argsText = inp.command;
          else if (inp.filePath) argsText = inp.filePath;
          else if (inp.prompt) argsText = inp.prompt;
          else argsText = JSON.stringify(input, null, 2);

          let title = st.title || st.metadata?.description || inp.description || '';

          // For edit tools, generate a unified diff from oldString/newString
          let resultText = '';
          const toolName = pd.tool || 'unknown';
          const isEdit = toolName === 'edit' || toolName === 'mcp_edit';
          const isRead = toolName === 'read' || toolName === 'mcp_read';
          const isWrite = toolName === 'write' || toolName === 'mcp_write' || toolName === 'mcp_Write';
          if (isWrite && inp.content) {
            // Show the written content as a full-addition diff (all green lines)
            const writeTarget = inp.filePath || title || 'file';
            title = 'Write ' + writeTarget;
            argsText = ''; // diff is shown as result, no need for args
            const lines = (inp.content as string).split('\n');
            const maxLn = String(lines.length).length;
            const pad = (s: string, w: number) => s.padStart(w, ' ');
            resultText = lines
              .map((line: string, i: number) => `${' '.repeat(maxLn)}  ${pad(String(i + 1), maxLn)}  + ${line}`)
              .join('\n');
          } else if (isEdit && inp.oldString && inp.newString) {
            const editTarget = inp.filePath || title || 'file';
            title = 'Edit ' + editTarget;
            argsText = ''; // diff is shown as result, no need for args
            // Prefer the full before/after file contents from filediff
            // metadata when available. This lets simpleDiff compute real
            // surrounding context, so even a single-line change shows a
            // few lines around it instead of just the changed line.
            const fd = st.metadata?.filediff;
            if (fd && typeof fd.before === 'string' && typeof fd.after === 'string') {
              resultText = simpleDiff(fd.before, fd.after, 1);
            } else {
              // Fallback: diff just oldString vs newString. Try to
              // determine the starting line number from the tool
              // output. The output often contains the modified
              // content prefixed with line numbers like "123: code".
              let startLine = 1;
              const outputText = typeof st.output === 'string' ? st.output : '';
              const contentMatch = outputText.match(/<content>\n?(\d+): /);
              if (contentMatch) {
                startLine = parseInt(contentMatch[1], 10) || 1;
              } else {
                const lineRefMatch = outputText.match(/[Ll]ine\s+(\d+)/);
                if (lineRefMatch) {
                  startLine = parseInt(lineRefMatch[1], 10) || 1;
                }
              }
              resultText = simpleDiff(inp.oldString, inp.newString, startLine);
            }
          } else if (isRead) {
            // Render reads as a muted inline line, not a collapsible
            // block. Paths are shown relative to the session's project
            // directory when possible.
            const readTarget = inp.filePath || argsText || title || 'file';
            const displayPath = relativizePath(readTarget, projectDirectory || '');
            const params: string[] = [];
            if (inp.offset) params.push(`offset=${inp.offset}`);
            if (inp.limit) params.push(`limit=${inp.limit}`);
            const suffix = params.length > 0 ? ` [${params.join(', ')}]` : '';
            toolCalls.push({
              type: 'tool-call' as const,
              toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
              toolName: '__read__',
              argsText: `Read ${displayPath}${suffix}`,
              result: undefined,
            });
            break;
          } else if (toolName === 'Skill' || toolName === 'skill' || toolName === 'mcp_Skill' || toolName === 'mcp_skill') {
            // Skill loads collapse to a single muted line in the
            // Read/Grep style — the input is just the skill name and
            // the output is the whole skill body, neither of which
            // is interesting in the thread.
            const skillName = inp.name || inp.skill || title || 'unknown';
            toolCalls.push({
              type: 'tool-call' as const,
              toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
              toolName: '__skill__',
              argsText: `Skill "${skillName}"`,
              result: undefined,
            });
            break;
          } else if (
            toolName === 'task' ||
            toolName === 'mcp_task' ||
            toolName === 'Task' ||
            toolName === 'mcp_Task'
          ) {
            // Render subagent calls without the prompt, with a link
            // to the session.
            const desc = inp.description || title || 'Subagent task';
            const agentType = inp.subagent_type || '';
            const label = agentType ? `${desc} (${agentType})` : desc;
            const taskId = extractTaskId(st);
            let taskOutput = '';
            const status = (st.status as string) || 'running';
            if (typeof st.output === 'string' && st.output.trim()) {
              // Claude Code wraps the final output in <task_result>
              // tags; strip the OpenCode task_id line if present.
              taskOutput = truncate(
                st.output.replace(/task_id:\s*ses_[^\s)]+[^\n]*\n?/, '').trim(),
                5000,
              );
            }
            // While running, inject live output from the task's
            // session so the main thread can render a small
            // streaming container until the final output is available.
            let livePreview = '';
            if (status === 'running' && taskId && taskLiveOutput?.[taskId]) {
              const lines = taskLiveOutput[taskId].split('\n');
              livePreview = lines.slice(-40).join('\n'); // tail of stdout
            }
            // Live tool list comes from the Claude Code hook cache,
            // injected by the backend into state.metadata.liveTools
            // for the most recent running Task tool_use.
            type LiveTool = { toolName: string; summary?: string; subagentId?: string; startedAt?: string };
            let liveTools: LiveTool[] = [];
            if (status === 'running' && st.metadata) {
              const meta = st.metadata as Record<string, unknown>;
              if (Array.isArray(meta.liveTools)) {
                liveTools = (meta.liveTools as LiveTool[]).filter(
                  (t) => t && typeof t.toolName === 'string' && t.toolName !== '',
                );
              }
            }
            toolCalls.push({
              type: 'tool-call' as const,
              toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
              toolName: '__task__',
              argsText: `${status}\n${label}`,
              result: JSON.stringify({ taskId, taskOutput, livePreview, liveTools }),
            });
            break;
          } else if (toolName === 'question' || toolName === 'mcp_question' || toolName === 'Question') {
            // Render questions as a special interactive-looking card.
            const questionsData = inp.questions || input?.questions;
            let questionsJson = '';
            if (questionsData) {
              questionsJson = typeof questionsData === 'string' ? questionsData : JSON.stringify(questionsData);
            } else {
              questionsJson = JSON.stringify(input);
            }
            toolCalls.push({
              type: 'tool-call' as const,
              toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
              toolName: '__question__',
              argsText: `${st.status || 'running'}\n${questionsJson}`,
              result:
                typeof st.output === 'string' && st.output.trim()
                  ? st.output
                  : st.output
                    ? JSON.stringify(st.output)
                    : undefined,
            });
            break;
          } else if (toolName === 'grep' || toolName === 'mcp_grep') {
            const grepPattern = inp.pattern || argsText || title || '';
            const include = inp.include ? ` (${inp.include})` : '';
            const grepText = grepPattern ? `Grep ${grepPattern}` : 'Grep';
            toolCalls.push({
              type: 'tool-call' as const,
              toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
              toolName: '__read__',
              argsText: `${grepText}${include}`,
              result: undefined,
            });
            break;
          } else if (toolName === 'glob' || toolName === 'mcp_glob') {
            const pattern = inp.pattern || argsText || title || '';
            const path = inp.path ? ` (${inp.path})` : '';
            const globText = pattern ? `Glob ${pattern}` : 'Glob';
            toolCalls.push({
              type: 'tool-call' as const,
              toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
              toolName: '__read__',
              argsText: `${globText}${path}`,
              result: undefined,
            });
            break;
          } else if (toolName === 'webfetch' || toolName === 'mcp_webfetch' || toolName === 'mcp_Webfetch') {
            const url = inp.url || argsText || title || '';
            const fetchText = url ? `Fetch ${url}` : 'Webfetch';
            toolCalls.push({
              type: 'tool-call' as const,
              toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
              toolName: '__read__',
              argsText: fetchText,
              result: undefined,
            });
            break;
          } else if (typeof st.output === 'string') {
            resultText = truncate(st.output, 5000);
          } else if (st.output != null) {
            resultText = truncate(JSON.stringify(st.output, null, 2), 5000);
          }

          toolCalls.push({
            type: 'tool-call' as const,
            toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
            toolName,
            argsText: `${st.status || 'running'}\n${title ? title + '\n' : ''}${argsText}`,
            result: resultText || undefined,
          });

          // Extract image attachments from tool results (e.g.
          // screenshot tools).
          if (st.attachments && Array.isArray(st.attachments)) {
            for (const att of st.attachments as FilePart[]) {
              if (isImageMime(att.mime) && att.url) {
                imageParts.push({ type: 'image' as const, image: att.url });
              }
            }
          }
          break;
        }
        case 'reasoning':
          if (pd.text?.trim()) {
            textPieces.push(`> *Reasoning:* ${pd.text}`);
          }
          break;
        case 'patch': {
          const file = pd.file || pd.path || 'unknown file';
          const diff = pd.content || pd.diff || '';
          if (diff) {
            textPieces.push(`**${file}**\n\`\`\`diff\n${diff}\n\`\`\``);
          }
          break;
        }
        case 'file': {
          // Image / file parts from OpenCode — render images inline.
          if (isImageMime(pd.mime) && pd.url) {
            imageParts.push({ type: 'image' as const, image: pd.url });
          } else if (pd.url && pd.filename) {
            // Non-image file - show as a text link/label.
            textPieces.push(`📎 ${pd.filename} (${pd.mime || 'file'})`);
          }
          break;
        }
        default: {
          // Treat unrecognized part types as tool-like operations so
          // they still appear in the UI (e.g. "write", "file",
          // custom tools).
          const st = pd.state || {};
          const input = st.input || {};
          const inp = input as Record<string, string>;
          const toolName = pd.tool || pd.type || 'unknown';
          let title = st.title || st.metadata?.description || inp.description || '';
          if (!title && inp.filePath) {
            title = toolName + ' ' + (inp.filePath.split('/').pop() || inp.filePath);
          }
          let argsText = '';
          if (typeof input === 'string') argsText = input;
          else if (inp.command) argsText = inp.command;
          else if (inp.filePath) argsText = inp.filePath;
          else {
            const s = JSON.stringify(input, null, 2);
            if (s !== '{}') argsText = s;
          }
          let resultText = '';
          if (typeof st.output === 'string') {
            resultText = truncate(st.output, 5000);
          } else if (st.output != null) {
            resultText = truncate(JSON.stringify(st.output, null, 2), 5000);
          }
          toolCalls.push({
            type: 'tool-call' as const,
            toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
            toolName,
            argsText: `${st.status || 'running'}\n${title ? title + '\n' : ''}${argsText}`,
            result: resultText || undefined,
          });
          break;
        }
      }
    });

    // If the message has an error object, inject the error details
    // as visible text — but skip abort errors since the UI already
    // shows an "interrupted" indicator.
    if (role === 'assistant' && m.data.error) {
      const errName = m.data.error.name || 'Error';
      const isAbort = errName === 'MessageAbortedError' || errName === 'AbortError';
      if (!isAbort) {
        const errMessage = m.data.error.data?.message || 'An unknown error occurred';
        textPieces.push(`**${errName}:** ${errMessage}`);
      }
    }

    // User messages cannot contain tool-call parts in assistant-ui.
    const visibleToolCalls = role === 'assistant' ? toolCalls : [];

    const msgStatus = role === 'assistant'
      ? (m.data.finish === 'error' || m.data.error)
        ? { type: 'incomplete' as const, reason: 'error' as const }
        : m.data.finish
          ? { type: 'complete' as const, reason: 'stop' as const }
          : { type: 'running' as const }
      : undefined;

    const failedEntry = role === 'user' ? failedById?.[m.id] : undefined;
    const customMeta = {
      ...(isQueued ? { queued: true } : {}),
      ...(m.data.tokens ? { tokens: m.data.tokens } : {}),
      ...(m.data.time ? { time: m.data.time } : {}),
      ...(m.data.error ? { errorName: m.data.error.name || 'Error' } : {}),
      ...(msgAgent ? { agent: msgAgent } : {}),
      ...(failedEntry ? { failed: { error: failedEntry.error, imagesDropped: !!failedEntry.imagesDropped } } : {}),
    };
    const metadata = Object.keys(customMeta).length > 0 ? { custom: customMeta } : undefined;

    // Build the final ThreadMessageLike result.
    let result: ThreadMessageLike;

    // If only text (no tool calls or images), use simple string content.
    if (visibleToolCalls.length === 0 && imageParts.length === 0) {
      result = {
        role,
        id: m.id,
        content: textPieces.join('\n\n') || '',
        createdAt: new Date(m.timeCreated),
        status: msgStatus,
        ...(metadata ? { metadata } : {}),
      };
    } else {
      // Mix of text, images, and tool calls.
      const content: ThreadMessageLike['content'] = [];
      if (textPieces.length > 0) {
        (content as Array<{ type: 'text'; text: string }>).push({ type: 'text', text: textPieces.join('\n\n') });
      }
      imageParts.forEach((img) => {
        (content as Array<unknown>).push(img);
      });
      visibleToolCalls.forEach((tc) => {
        (content as Array<unknown>).push(tc);
      });

      result = {
        role,
        id: m.id,
        content,
        createdAt: new Date(m.timeCreated),
        status: msgStatus,
        ...(metadata ? { metadata } : {}),
      };
    }

    // Store in per-message cache.
    convertedMessageCache.set(m, {
      parts: msgPartsRaw,
      pendingAgent,
      taskLiveOutput,
      projectDirectory,
      failedById,
      isQueued,
      msgAgent,
      result,
    });

    return result;
  });

  // Return the previous result array when every element is the same
  // reference. This prevents useSyncExternalStore (inside
  // @assistant-ui/react's useExternalStoreRuntime) from seeing a new
  // snapshot on every call, which would trigger a forceStoreRerender
  // loop during the passive-effect phase.
  if (
    lastConvertedResult &&
    lastConvertedResult.length === result.length &&
    result.every((r, i) => r === lastConvertedResult[i])
  ) {
    return lastConvertedResult;
  }
  lastConvertedResult = result;
  return result;
}
