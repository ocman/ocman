import { useMemo } from 'react';
import {
  AssistantRuntimeProvider,
  useExternalStoreRuntime,
  type ThreadMessageLike,
} from '@assistant-ui/react';
import type { Message, Part, PartData, FilePart } from '../lib/api';
import { api } from '../lib/api';
import { simpleDiff } from '../lib/diff';

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

      // A user message is "queued" when it follows an assistant message that
      // hasn't finished yet (still streaming / no finish reason and no error).
      // This means the user sent input while the assistant was still working.
      let isQueued = false;
      if (role === 'user' && idx > 0) {
        const prev = filtered[idx - 1];
        if (
          prev.data?.role === 'assistant' &&
          !prev.data.finish &&
          !prev.data.error
        ) {
          isQueued = true;
        }
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
            if (isEdit && inp.oldString && inp.newString) {
              const fileName = inp.filePath ? inp.filePath.split('/').pop() || inp.filePath : '';
              title = 'Edited ' + fileName;
              argsText = ''; // diff is shown as result, no need for args
              resultText = simpleDiff(inp.oldString, inp.newString);
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
            } else if (toolName === 'task' || toolName === 'mcp_task') {
              // Render subagent calls without the prompt, with a link to the session
              const desc = inp.description || title || 'Subagent task';
              const agentType = inp.subagent_type || '';
              const label = agentType ? `${desc} (${agentType})` : desc;
              // Extract task_id and output from the tool result
              let taskId = '';
              let taskOutput = '';
              const outputStr = typeof st.output === 'string' ? st.output : JSON.stringify(st.output || '');
              // task_id may appear in the output text
              const idMatch = outputStr.match(/task_id:\s*(ses_[^\s)]+)/);
              if (idMatch) taskId = idMatch[1];
              if (!taskId && st.output && typeof st.output === 'object') {
                const out = st.output as Record<string, unknown>;
                if (typeof out.task_id === 'string') taskId = out.task_id;
              }
              // The output is the subagent result — use it directly, stripping the task_id line
              if (typeof st.output === 'string' && st.output.trim()) {
                taskOutput = truncate(
                  st.output.replace(/task_id:\s*ses_[^\s)]+[^\n]*\n?/, '').trim(),
                  5000,
                );
              }
              toolCalls.push({
                type: 'tool-call' as const,
                toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
                toolName: '__task__',
                argsText: `${st.status || 'running'}\n${label}`,
                result: JSON.stringify({ taskId, taskOutput }),
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

      // If the message has an error object, inject the error details as visible text.
      if (role === 'assistant' && m.data.error) {
        const errName = m.data.error.name || 'Error';
        const errMessage = m.data.error.data?.message || 'An unknown error occurred';
        textPieces.push(`**${errName}:** ${errMessage}`);
      }

      // User messages cannot contain tool-call parts in assistant-ui.
      const visibleToolCalls = role === 'assistant' ? toolCalls : [];

      // If only text (no tool calls or images), use simple string content
      if (visibleToolCalls.length === 0 && imageParts.length === 0) {
        return {
          role,
          id: m.id,
          content: textPieces.join('\n\n') || '',
          createdAt: new Date(m.timeCreated),
          status: role === 'assistant'
            ? (m.data.finish === 'error' || m.data.error)
              ? { type: 'incomplete' as const, reason: 'error' as const }
              : m.data.finish
                ? { type: 'complete' as const, reason: 'stop' as const }
                : { type: 'running' as const }
            : undefined,
          ...(isQueued ? { metadata: { custom: { queued: true } } } : {}),
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
        status: role === 'assistant'
          ? (m.data.finish === 'error' || m.data.error)
            ? { type: 'incomplete' as const, reason: 'error' as const }
            : m.data.finish
              ? { type: 'complete' as const, reason: 'stop' as const }
              : { type: 'running' as const }
          : undefined,
        ...(isQueued ? { metadata: { custom: { queued: true } } } : {}),
      };
    });
}

interface Props {
  messages: Message[];
  parts: Part[];
  sessionId: string;
  directory: string;
  portAvailable: boolean;
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
  directory,
  portAvailable,
  children,
}: Props) {
  const converted = useMemo(
    () => convertMessages(messages, parts),
    [messages, parts],
  );

  const isRunning = useMemo(() => computeIsRunning(messages), [messages]);

  const runtime = useExternalStoreRuntime({
    messages: converted,
    isRunning,
    convertMessage: (m: ThreadMessageLike) => m,
    onNew: async (message) => {
      if (!portAvailable) return;
      const textPart = message.content.find((c) => c.type === 'text');
      const text = textPart && textPart.type === 'text' ? textPart.text : '';
      const imageParts = message.content
        .filter((c): c is { type: 'image'; image: string } => c.type === 'image' && 'image' in c)
        .map((c) => ({ url: c.image, mime: 'image/png' }));
      if (!text && imageParts.length === 0) return;
      await api.sendMessage(sessionId, directory, text, imageParts.length > 0 ? imageParts : undefined);
    },
  });

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      {children}
    </AssistantRuntimeProvider>
  );
}
