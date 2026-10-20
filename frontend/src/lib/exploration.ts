import type { ThreadMessage } from '@assistant-ui/react';

type ContentPart = ThreadMessage['content'][number];
export type ExplorationPart = Extract<ContentPart, { type: 'tool-call' }>;
export type ExplorationMessage = Pick<ThreadMessage, 'id' | 'role' | 'content' | 'metadata'>;
export interface ExplorationEntry {
  messageId: string;
  part: ExplorationPart;
}

export function explorationKind(part: ContentPart): 'search' | 'file' | null {
  if (part.type !== 'tool-call') return null;
  if ((part.artifact as { ocmanApprovals?: unknown[] } | undefined)?.ocmanApprovals?.length) return null;
  const name = part.toolName.replace(/^mcp_/i, '').toLowerCase();
  if (name === 'read') return 'file';
  if (name === 'grep' || name === 'glob') return 'search';
  if (name !== '__read__') return null;
  if (/^Read\b/.test(part.argsText)) return 'file';
  return /^(Grep|Glob)\b/.test(part.argsText) ? 'search' : null;
}

function onlyExploration(message: ExplorationMessage): boolean {
  return message.role === 'assistant'
    && message.content.some(part => explorationKind(part))
    && message.content.every(part => explorationKind(part) || (part.type === 'text' && !part.text.trim()));
}

function continuesExploration(previous: ExplorationMessage, next: ExplorationMessage): boolean {
  return onlyExploration(previous) && onlyExploration(next)
    && !next.metadata?.custom?.modelChangedTo && !next.metadata?.custom?.timelineAt;
}

/** Collect adjacent lookup-only messages without changing message IDs or turn statistics. */
export function messageExploration(messages: readonly ExplorationMessage[], id: string): ExplorationEntry[] | null {
  const index = messages.findIndex(message => message.id === id);
  if (index < 0 || !onlyExploration(messages[index])) return null;
  if (index > 0 && continuesExploration(messages[index - 1], messages[index])) return [];
  const entries: ExplorationEntry[] = [];
  for (let i = index; i < messages.length; i++) {
    if (i > index && !continuesExploration(messages[i - 1], messages[i])) break;
    for (const part of messages[i].content) {
      if (part.type === 'tool-call' && explorationKind(part)) entries.push({ messageId: messages[i].id, part });
    }
  }
  return entries;
}

export function explorationRunning(part: ExplorationPart): boolean {
  const artifact = part.artifact as { explorationStatus?: string } | undefined;
  const status = artifact?.explorationStatus || part.argsText.split('\n')[0];
  return status === 'running' || status === 'pending';
}
