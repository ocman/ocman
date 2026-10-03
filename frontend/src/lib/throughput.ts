import type { Message, Part, PartData, TaskSessionData } from './api';
import { extractTaskId, isTaskTool } from './taskId';

function partData(part: Part): PartData | null {
  try {
    return typeof part.data === 'string' ? JSON.parse(part.data) : part.data;
  } catch {
    return null;
  }
}

// Request-time estimate: startup/prefill remain included. Completed samples
// avoid pairing final token counts with a clock that keeps ticking during waits.
export function throughputSample(message: Message, parts: PartData[]): [number, number] {
  const { role, time, tokens, finish } = message.data;
  const start = time?.created;
  const end = time?.completed;
  if (role !== 'assistant' || !start || !end || end <= start || !tokens?.output) return [0, 0];
  const tools = parts.filter(p => p.type === 'tool');
  if (finish === 'tool-calls' && tools.length === 0) return [0, 0];
  const intervals: [number, number][] = [];
  for (const tool of tools) {
    const { start: from = 0, end: to = 0 } = tool.state?.time ?? {};
    if (from <= 0 || to <= 0 || !Number.isFinite(from + to) || to < from) return [0, 0];
    intervals.push([Math.max(start, from), Math.min(end, to)]);
  }
  intervals.sort((a, b) => a[0] - b[0]);
  let cursor = start;
  let waiting = 0;
  for (const [from, to] of intervals) {
    waiting += Math.max(0, to - Math.max(cursor, from));
    cursor = Math.max(cursor, to);
  }
  const duration = end - start - waiting;
  return duration > 100 ? [tokens.output, duration] : [0, 0];
}

export function estimateThroughput(
  messages: Message[],
  parts: Part[],
  tasks: Record<string, TaskSessionData> = {},
): number | null {
  const lastUser = messages.findLastIndex(m => m.data.role === 'user');
  const current = messages.slice(lastUser + 1);
  const since = messages[lastUser]?.data.time?.created ?? messages[lastUser]?.timeCreated ?? 0;
  let output = 0;
  let duration = 0;
  const visited = new Set<string>();
  function add(messages: Message[], parts: Part[]) {
    const byMessage = new Map<string, PartData[]>();
    for (const part of parts) {
      const data = partData(part);
      if (!data) continue;
      const entries = byMessage.get(part.messageId) ?? [];
      entries.push(data);
      byMessage.set(part.messageId, entries);
    }
    for (const message of messages) {
      const key = `${message.sessionId}:${message.id}`;
      if (visited.has(key) || (message.data.time?.created ?? message.timeCreated) < since) continue;
      visited.add(key);
      const messageParts = byMessage.get(message.id) ?? [];
      const [tokens, ms] = throughputSample(message, messageParts);
      output += tokens;
      duration += ms;
      for (const part of messageParts) {
        if (!isTaskTool(part.tool)) continue;
        const id = extractTaskId(part.state);
        const task = id ? tasks[id] : undefined;
        if (task) add(task.messages, task.parts);
      }
    }
  }
  add(current, parts);
  return output > 0 && duration > 100 ? output / (duration / 1000) : null;
}
