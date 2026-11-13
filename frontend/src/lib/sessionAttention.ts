import type { NotifyEntry, Session } from './api';
import { hiddenSessionOrigins, matchesSessionListFilters, type SessionListFilters } from './sessionListFilters';

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

/** Recent mode has no notification-only fallback, so count only its current rows. */
export function recentSessionAttentionEntries(entries: readonly NotifyEntry[], sessions: readonly Session[], filters: SessionListFilters): NotifyEntry[] {
  const hidden = hiddenSessionOrigins(sessions, filters);
  const rows = new Map(sessions.map(session => [sessionAttentionKey(session), session]));
  return attentionSessionEntries(entries).filter(entry => {
    const session = rows.get(sessionAttentionKey(entry));
    if (!session || !matchesSessionListFilters(session, filters, hidden)) return false;
    return !!(entry.pendingPermission || entry.pendingQuestion ||
      session.status !== 'busy' && !session.seen &&
      (session.backgroundTaskCount ?? 0) <= (session.backgroundServiceCount ?? 0));
  });
}
