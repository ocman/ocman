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

/**
 * The compact list keeps unarchived rows older than its time window, while the
 * accounting read drops everything updated before its cutoff. Read back to the
 * oldest displayed row (floored to a minute so the query key stays stable).
 */
export function accountingSince(compact: readonly Session[], sinceHours: number | undefined): number | undefined {
  if (sinceHours === undefined || !compact.length) return undefined;
  const oldest = Math.min(...compact.map(s => s.timeUpdated || 0));
  return oldest > 0 ? Math.floor(oldest / 60_000) * 60_000 : undefined;
}

/** Accounting can involve slow platform enrichment. Never gate browsing on it. */
export function useSessionAccounting(compact: readonly Session[], params: { sinceHours?: number; dir?: string }, enabled = true) {
  const since = accountingSince(compact, params.sinceHours);
  const accountingQ = useSessions({ dir: params.dir, since, limit: 0, accounting: true },
    { poll: true, enabled: enabled && compact.length > 0 });
  return {
    ...mergeSessionAccounting(compact, accountingQ.data),
    error: accountingQ.error instanceof Error ? accountingQ.error.message : accountingQ.error ? 'Unknown error' : null,
    retry: () => { void accountingQ.refetch(); },
    retrying: accountingQ.isFetching,
  };
}
