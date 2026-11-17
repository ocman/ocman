import type { Session } from './api';
import { useSessions } from './queries';

/** Owner-qualified row key; IDs alone can collide across owners. */
export const sessionAccountingKey = (session: Pick<Session, 'platform' | 'id'>) => `${session.platform}:${session.id}`;

/**
 * Compact (`view=sidebar`) rows never carry accounting: their message counts,
 * durations and costs are zero placeholders. Copy totals from the accounting
 * read and report rows whose totals are missing or older than the compact row
 * as pending, so lists never present placeholders as real zeros.
 */
export function mergeSessionAccounting(compact: readonly Session[], accounting: readonly Session[] | undefined) {
  const totals = new Map((accounting ?? []).map(s => [sessionAccountingKey(s), s]));
  const pendingKeys = new Set<string>();
  const sessions = compact.map(s => {
    const key = sessionAccountingKey(s);
    const t = totals.get(key);
    if (!t || t.timeUpdated < s.timeUpdated) {
      pendingKeys.add(key);
      return s;
    }
    // Compact rows remain authoritative for status, archive flags and titles.
    return { ...s, messageCount: t.messageCount, durationMs: t.durationMs, activeDurationMs: t.activeDurationMs,
      totalInputTokens: t.totalInputTokens, totalOutputTokens: t.totalOutputTokens,
      totalCost: t.totalCost, totalEstCost: t.totalEstCost, totalEffectiveCost: t.totalEffectiveCost };
  });
  return { sessions, pendingKeys };
}

/** Accounting can involve slow platform enrichment. Never gate browsing on it. */
export function useSessionAccounting(compact: readonly Session[], params: { sinceHours?: number; dir?: string }, enabled = true) {
  const accountingQ = useSessions({ ...params, limit: 0, accounting: true }, { poll: true, enabled });
  return mergeSessionAccounting(compact, accountingQ.data);
}
