import type { NotifyEntry } from './api';

export function sessionAttentionKey(entry: { platform?: string; id: string }): string {
  return `${entry.platform ?? ''}:${entry.id}`;
}

export function needsSessionAttention(entry: NotifyEntry): boolean {
  return !!(entry.pendingPermission || entry.pendingQuestion || (!entry.seen && !entry.suppressTerminal));
}

/** The badge and its visible session list must use the same eligibility rules. */
export function attentionSessionEntries(entries: readonly NotifyEntry[]): NotifyEntry[] {
  const distinct = new Map<string, NotifyEntry>();
  for (const entry of entries) {
    if (needsSessionAttention(entry)) {
      const key = sessionAttentionKey(entry);
      if (!distinct.has(key)) distinct.set(key, entry);
    }
  }
  return [...distinct.values()];
}
