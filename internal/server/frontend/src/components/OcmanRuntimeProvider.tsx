import { useMemo } from 'react';
import {
  AssistantRuntimeProvider,
  useExternalStoreRuntime,
  type ThreadMessageLike,
} from '@assistant-ui/react';
import type { Message, Part, PartData } from '../lib/api';
import { api } from '../lib/api';

function parsePart(p: Part): PartData {
  try {
    return typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
  } catch {
    return (p.data || {}) as PartData;
  }
}

// Generate a simple unified diff between two strings.
// Shows context lines around changes for readability.
function simpleDiff(oldStr: string, newStr: string): string {
  const oldLines = oldStr.split('\n');
  const newLines = newStr.split('\n');

  // Find longest common subsequence to produce a proper diff
  const m = oldLines.length;
  const n = newLines.length;

  // For small inputs, use full LCS. For large inputs, just show removed/added.
  if (m + n > 200) {
    const out: string[] = [];
    oldLines.forEach(l => out.push(`- ${l}`));
    newLines.forEach(l => out.push(`+ ${l}`));
    return out.join('\n');
  }

  // Build LCS table
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      dp[i][j] = oldLines[i - 1] === newLines[j - 1]
        ? dp[i - 1][j - 1] + 1
        : Math.max(dp[i - 1][j], dp[i][j - 1]);
    }
  }

  // Backtrack to produce diff
  const result: string[] = [];
  let i = m, j = n;
  const ops: Array<[string, string]> = [];
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && oldLines[i - 1] === newLines[j - 1]) {
      ops.push([' ', oldLines[i - 1]]);
      i--; j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      ops.push(['+', newLines[j - 1]]);
      j--;
    } else {
      ops.push(['-', oldLines[i - 1]]);
      i--;
    }
  }
  ops.reverse();

  // Assign line numbers to each op
  let oldLine = 1, newLine = 1;
  const numbered: Array<[string, string, string, string]> = []; // [op, text, oldLn, newLn]
  ops.forEach(([op, text]) => {
    if (op === ' ') {
      numbered.push([op, text, String(oldLine), String(newLine)]);
      oldLine++; newLine++;
    } else if (op === '-') {
      numbered.push([op, text, String(oldLine), '']);
      oldLine++;
    } else {
      numbered.push([op, text, '', String(newLine)]);
      newLine++;
    }
  });

  // Only show lines around changes (context of 2 lines)
  const ctx = 2;
  const changed = new Set<number>();
  numbered.forEach(([op], idx) => {
    if (op !== ' ') {
      for (let k = Math.max(0, idx - ctx); k <= Math.min(numbered.length - 1, idx + ctx); k++) {
        changed.add(k);
      }
    }
  });

  const pad = (s: string, w: number) => s.padStart(w, ' ');
  const maxOld = String(oldLine).length;
  const maxNew = String(newLine).length;

  let lastShown = -1;
  numbered.forEach(([op, text, oln, nln], idx) => {
    if (!changed.has(idx)) return;
    if (lastShown >= 0 && idx - lastShown > 1) {
      result.push(`${' '.repeat(maxOld)}  ${' '.repeat(maxNew)}    ...`);
    }
    const ol = oln ? pad(oln, maxOld) : ' '.repeat(maxOld);
    const nl = nln ? pad(nln, maxNew) : ' '.repeat(maxNew);
    result.push(`${ol}  ${nl}  ${op} ${text}`);
    lastShown = idx;
  });

  return result.join('\n');
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
              // For read tools, the file path is already in the result XML;
              // use short filename as title and suppress args to avoid repetition.
              const fileName = inp.filePath.split('/').pop() || inp.filePath;
              if (!title) title = 'Read ' + fileName;
              argsText = ''; // path is shown in result output
              if (typeof st.output === 'string') {
                resultText = truncate(st.output, 5000);
              } else if (st.output != null) {
                resultText = truncate(JSON.stringify(st.output, null, 2), 5000);
              }
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
            ? m.data.finish === 'stop'
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
          ? m.data.finish === 'stop'
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

// Determine if the session is actively running based on the last message
function computeIsRunning(messages: Message[]): boolean {
  if (messages.length === 0) return false;
  const last = messages[messages.length - 1];
  if (!last.data) return false;
  // If last message is from user, assistant hasn't replied yet -> running
  if (last.data.role === 'user') return true;
  // If last message is from assistant but not finished -> running
  if (last.data.role === 'assistant' && last.data.finish !== 'stop') return true;
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
