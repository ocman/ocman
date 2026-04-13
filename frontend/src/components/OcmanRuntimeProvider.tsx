import { useMemo } from 'react';
import {
  AssistantRuntimeProvider,
  useExternalStoreRuntime,
  type ThreadMessageLike,
} from '@assistant-ui/react';
import type { Message, Part, PartData } from '../lib/api';
import { api } from '../lib/api';
import { simpleDiff } from '../lib/diff';

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

  return messages
    .filter((m) => m.data?.role === 'user' || m.data?.role === 'assistant')
    .map((m): ThreadMessageLike => {
      const role = m.data.role as 'user' | 'assistant';
      const msgParts = (partsByMsg[m.id] || []).map(parsePart);

      // Build content as string | content array. Using string for simple text,
      // and the full content array format for messages with tool calls.
      const textPieces: string[] = [];
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
            } else if (isRead && inp.filePath) {
              // Render reads as a muted inline line, not a collapsible block.
              const fileName = inp.filePath.split('/').pop() || inp.filePath;
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
            } else if ((toolName === 'grep' || toolName === 'mcp_grep') && inp.pattern) {
              // Render grep as a muted inline line, like file reads.
              const include = inp.include ? ` (${inp.include})` : '';
              toolCalls.push({
                type: 'tool-call' as const,
                toolCallId: m.id + '-' + toolName + '-' + toolCalls.length,
                toolName: '__read__',
                argsText: `Grep ${inp.pattern}${include}`,
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

      // If only text, use simple string content
      if (toolCalls.length === 0) {
        return {
          role,
          id: m.id,
          content: textPieces.join('\n\n') || '',
          createdAt: new Date(m.timeCreated),
          status: role === 'assistant'
            ? m.data.finish
              ? { type: 'complete' as const, reason: 'stop' as const }
              : { type: 'running' as const }
            : undefined,
        };
      }

      // Mix of text and tool calls
      const content: ThreadMessageLike['content'] = [];
      if (textPieces.length > 0) {
        (content as Array<{ type: 'text'; text: string }>).push({ type: 'text', text: textPieces.join('\n\n') });
      }
      toolCalls.forEach((tc) => {
        (content as Array<unknown>).push(tc);
      });

      return {
        role,
        id: m.id,
        content,
        createdAt: new Date(m.timeCreated),
        status: role === 'assistant'
          ? m.data.finish
            ? { type: 'complete' as const, reason: 'stop' as const }
            : { type: 'running' as const }
          : undefined,
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
function computeIsRunning(messages: Message[]): boolean {
  if (messages.length === 0) return false;
  const last = messages[messages.length - 1];
  if (!last.data) return false;
  // If last message is from user, assistant hasn't replied yet -> running
  if (last.data.role === 'user') return true;
  // If last message is from assistant with no finish reason -> still streaming
  if (last.data.role === 'assistant' && !last.data.finish) return true;
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
      if (!textPart || textPart.type !== 'text') return;
      await api.sendMessage(sessionId, directory, textPart.text);
    },
  });

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      {children}
    </AssistantRuntimeProvider>
  );
}
