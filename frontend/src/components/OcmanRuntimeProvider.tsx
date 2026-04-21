import { useMemo } from 'react';
import {
  AssistantRuntimeProvider,
  useExternalStoreRuntime,
  type ThreadMessageLike,
} from '@assistant-ui/react';
import type { AgentInfo, Message, Part, PartData, FilePart } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { simpleDiff } from '../lib/diff';
import { AgentsContext } from '../lib/agentColor';

function isImageMime(mime: string | undefined): boolean {
  return !!mime && mime.startsWith('image/');
}

function parsePart(p: Part): PartData {
  try {
    return typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
  } catch {
    return (p.data || {}) as PartData;
  }
}

function truncate(text: string | undefined | null, max: number): string {
  if (!text) return '';
  if (text.length <= max) return text;
  return text.slice(0, max) + '\n... (' + text.length + ' chars total)';
}

function convertMessages(
  messages: Message[],
  parts: Part[],
  pendingAgent?: string,
  taskLiveOutput?: Record<string, string>,
): ThreadMessageLike[] {
  const partsByMsg: Record<string, Part[]> = {};
  parts.forEach((p) => {
    if (!partsByMsg[p.messageId]) partsByMsg[p.messageId] = [];
    partsByMsg[p.messageId].push(p);
  });

  const filtered = messages.filter((m) => m.data?.role === 'user' || m.data?.role === 'assistant');
  return filtered
    .map((m, idx): ThreadMessageLike => {
      const role = m.data.role as 'user' | 'assistant';

      // A user message is "queued" when it follows an unfinished assistant
      // turn and the session already had a prior user turn. New UI-created
      // sessions can start with an assistant bootstrap message, which should
      // not make the first user message look queued.
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

      // Resolve the agent associated with this message so the UI can color
      // it. For assistant messages the agent is on the message itself. For
      // user messages we attribute the color to the agent that replies —
      // i.e. the next assistant message in the thread — falling back to the
      // currently-selected agent when the reply hasn't been produced yet.
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
      const msgParts = (partsByMsg[m.id] || []).map(parsePart);

      // Build content as string | content array. Using string for simple text,
      // and the full content array format for messages with tool calls or images.
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
              // Prefer the full before/after file contents from filediff metadata
              // when available. This lets simpleDiff compute real surrounding
              // context, so even a single-line change shows a few lines around
              // it instead of just the changed line itself.
              const fd = st.metadata?.filediff;
              if (fd && typeof fd.before === 'string' && typeof fd.after === 'string') {
                resultText = simpleDiff(fd.before, fd.after, 1);
              } else {
                // Fallback: diff just oldString vs newString. Try to determine
                // the starting line number from the tool output. The output
                // often contains the modified content prefixed with line
                // numbers like "123: code\n124: more code".
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
              // Render reads as a muted inline line, not a collapsible block.
              const readTarget = inp.filePath || argsText || title || 'file';
              const fileName = readTarget.split('/').pop() || readTarget;
              const params: string[] = [];
              if (inp.offset) params.push(`offset=${inp.offset}`);
              if (inp.limit) params.push(`limit=${inp.limit}`);
              const suffix = params.length > 0 ? ` [${params.join(', ')}]` : '';
              toolCalls.push({
                type: 'tool-call' as const,
                toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
                toolName: '__read__',
                argsText: `Read ${fileName}${suffix}`,
                result: undefined,
              });
              break;
            } else if (
              toolName === 'task' ||
              toolName === 'mcp_task' ||
              toolName === 'Task' ||
              toolName === 'mcp_Task'
            ) {
              // Render subagent calls without the prompt, with a link to the session
              const desc = inp.description || title || 'Subagent task';
              const agentType = inp.subagent_type || '';
              const label = agentType ? `${desc} (${agentType})` : desc;
              // Extract task_id and output from the tool result.
              // The task_id can appear in several places depending on
              // whether the task is still running or already completed:
              // - inp.task_id: present when resuming an existing task
              // - st.output: written by OpenCode once the tool returns
              // - st.metadata: may contain session references
              let taskId = '';
              let taskOutput = '';
              // Check input first (available immediately for resumed tasks)
              if (typeof inp.task_id === 'string' && inp.task_id) taskId = inp.task_id;
              const outputStr = typeof st.output === 'string' ? st.output : JSON.stringify(st.output || '');
              // task_id may appear in the output text
              const idMatch = outputStr.match(/task_id:\s*(ses_[^\s)]+)/);
              if (idMatch) taskId = idMatch[1];
              if (!taskId && st.output && typeof st.output === 'object') {
                const out = st.output as Record<string, unknown>;
                if (typeof out.task_id === 'string') taskId = out.task_id;
              }
              // Check metadata for session references
              if (!taskId && st.metadata) {
                const meta = st.metadata as Record<string, unknown>;
                if (typeof meta.sessionId === 'string') taskId = meta.sessionId;
                else if (typeof meta.taskId === 'string') taskId = meta.taskId;
                else if (typeof meta.task_id === 'string') taskId = meta.task_id;
              }
              // The output is the subagent result — use it directly, stripping the task_id line
              const status = (st.status as string) || 'running';
              if (typeof st.output === 'string' && st.output.trim()) {
                // Claude Code wraps the final output in <task_result> tags;
                // strip the OpenCode task_id line if present. Keep both
                // transformations here so the renderer receives clean text.
                taskOutput = truncate(
                  st.output.replace(/task_id:\s*ses_[^\s)]+[^\n]*\n?/, '').trim(),
                  5000,
                );
              }
              // While running, inject live output from the task's session so
              // the main thread can render a small streaming container until
              // the final output is available. (OpenCode path.)
              let livePreview = '';
              if (status === 'running' && taskId && taskLiveOutput?.[taskId]) {
                const lines = taskLiveOutput[taskId].split('\n');
                livePreview = lines.slice(-40).join('\n'); // tail of stdout
              }
              // Live tool list comes from the Claude Code hook cache, injected
              // by the backend into state.metadata.liveTools for the most
              // recent running Task tool_use. Shape: [{toolName, summary,
              // subagentId, startedAt}, ...]. Ignored unless the Task is
              // actually running.
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
              // The questions data lives in the tool input as a JSON array.
              const questionsData = inp.questions || input?.questions;
              let questionsJson = '';
              if (questionsData) {
                questionsJson = typeof questionsData === 'string' ? questionsData : JSON.stringify(questionsData);
              } else {
                // Fallback: try the full input as JSON
                questionsJson = JSON.stringify(input);
              }
              toolCalls.push({
                type: 'tool-call' as const,
                toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
                toolName: '__question__',
                argsText: `${st.status || 'running'}\n${questionsJson}`,
                result: typeof st.output === 'string' && st.output.trim() ? st.output : st.output ? JSON.stringify(st.output) : undefined,
              });
              break;
            } else if (toolName === 'grep' || toolName === 'mcp_grep') {
              // Render grep as a muted inline line, like file reads.
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
              // Render glob as a muted inline line, like file reads/greps.
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
              // Render webfetch as a muted inline line, like file reads.
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

            // Extract image attachments from tool results (e.g. screenshot tools)
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
            // Image/file parts from OpenCode - render images inline
            if (isImageMime(pd.mime) && pd.url) {
              imageParts.push({ type: 'image' as const, image: pd.url });
            } else if (pd.url && pd.filename) {
              // Non-image file - show as a text link/label
              textPieces.push(`📎 ${pd.filename} (${pd.mime || 'file'})`);
            }
            break;
          }
          default: {
            // Treat unrecognized part types as tool-like operations so they
            // still appear in the UI (e.g. "write", "file", custom tools).
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

      // If the message has an error object, inject the error details as visible text —
      // but skip abort errors since the UI already shows an "interrupted" indicator.
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

      const customMeta = {
        ...(isQueued ? { queued: true } : {}),
        ...(m.data.tokens ? { tokens: m.data.tokens } : {}),
        ...(m.data.time ? { time: m.data.time } : {}),
        ...(m.data.error ? { errorName: m.data.error.name || 'Error' } : {}),
        ...(msgAgent ? { agent: msgAgent } : {}),
      };
      const metadata = Object.keys(customMeta).length > 0 ? { custom: customMeta } : undefined;

      // If only text (no tool calls or images), use simple string content
      if (visibleToolCalls.length === 0 && imageParts.length === 0) {
        return {
          role,
          id: m.id,
          content: textPieces.join('\n\n') || '',
          createdAt: new Date(m.timeCreated),
          status: msgStatus,
          ...(metadata ? { metadata } : {}),
        };
      }

      // Mix of text, images, and tool calls
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

      return {
        role,
        id: m.id,
        content,
        createdAt: new Date(m.timeCreated),
        status: msgStatus,
        ...(metadata ? { metadata } : {}),
      };
    });
}

interface Props {
  messages: Message[];
  parts: Part[];
  sessionId: string;
  /**
   * Whether the composer may currently send messages. Typically this
   * is `platformCapabilities.composer && portAvailable` — that is, the
   * owning platform supports composition AND the live connection is up.
   * When false, `onNew` is a no-op.
   */
  canSend: boolean;
  // Agent that the user is about to send the next message as. Used to color
  // user messages that haven't been replied to yet.
  pendingAgent?: string;
  // Agent metadata (including colors) loaded from the OpenCode /agent API.
  agents?: AgentInfo[];
  // Live stdout from running task sessions. Maps taskId -> last 10 lines of output.
  taskLiveOutput?: Record<string, string>;
  children: React.ReactNode;
}

// Determine if the session is actively running based on the last message.
// The assistant is running if the last message has no finish reason (still streaming).
// Any finish value ("stop", "tool-calls", etc.) means that turn is done.
// A message with an error object is also not running.
function computeIsRunning(messages: Message[]): boolean {
  if (messages.length === 0) return false;
  const last = messages[messages.length - 1];
  if (!last.data) return false;
  // If last message is from user, assistant hasn't replied yet -> running
  if (last.data.role === 'user') return true;
  // If last message is from assistant with no finish reason and no error -> still streaming
  if (last.data.role === 'assistant' && !last.data.finish && !last.data.error) return true;
  return false;
}

export function OcmanRuntimeProvider({
  messages,
  parts,
  sessionId,
  canSend,
  pendingAgent,
  agents,
  taskLiveOutput,
  children,
}: Props) {
  const agentList = useMemo(() => agents ?? [], [agents]);
  const sendMessage = useApiStore((state) => state.sendMessage);
  const converted = useMemo(
    () => convertMessages(messages, parts, pendingAgent, taskLiveOutput),
    [messages, parts, pendingAgent, taskLiveOutput],
  );

  const isRunning = useMemo(() => computeIsRunning(messages), [messages]);

  const runtime = useExternalStoreRuntime({
    messages: converted,
    isRunning,
    convertMessage: (m: ThreadMessageLike) => m,
    onNew: async (message) => {
      if (!canSend) return;
      const textPart = message.content.find((c) => c.type === 'text');
      const text = textPart && textPart.type === 'text' ? textPart.text : '';
      const imageParts = message.content
        .filter((c): c is { type: 'image'; image: string } => c.type === 'image' && 'image' in c)
        .map((c) => ({ url: c.image, mime: 'image/png' }));
      if (!text && imageParts.length === 0) return;
      await sendMessage(sessionId, text, imageParts.length > 0 ? imageParts : undefined);
    },
  });

  return (
    <AgentsContext.Provider value={agentList}>
      <AssistantRuntimeProvider runtime={runtime}>
        {children}
      </AssistantRuntimeProvider>
    </AgentsContext.Provider>
  );
}
