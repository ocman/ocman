import type { Session } from './api';

/**
 * Compute a cheap dedup hash for a sidebar session list. Used to skip
 * `setState` calls when a poll returned identical data — the
 * SessionDetail page rebuilds its memoised project groups whenever
 * `recentSessions` changes, so avoiding spurious re-renders matters.
 *
 * Hash format: pipe-separated `(id, status, timeUpdated, p?, q?)` per
 * session, joined by commas. Two arrays produce the same hash iff
 * every entry has the same id / status / timestamp / pending flags.
 */
export function computeSidebarHash(sessions: readonly Session[]): string {
  return sessions
    .map(
      (s) =>
        `${s.id}|${s.status}|${s.timeUpdated}|${s.pendingPermission ? 'p' : ''}${s.pendingQuestion ? 'q' : ''}`,
    )
    .join(',');
}

/**
 * Aggregate "what should this group's status dot show?" derived from
 * the sessions inside it. The rollup follows a strict priority order
 * so a single noisy session can not hide a more important state on a
 * sibling.
 *
 * Priority:
 *   1. `pending` — any session has an unanswered prompt.
 *   2. `error`   — any unseen errored session.
 *   3. `busy`    — any session is actively running.
 *   4. `waiting` — any unseen waiting session.
 *   5. `none`    — nothing notable.
 *
 * `effectiveStatusOf` lets the caller override the recorded status of
 * any individual session — used by `SessionDetail` to fold the
 * page's optimistic status into its own row before rolling up.
 */
export type GroupAggregate =
  | { kind: 'none' }
  | { kind: 'waiting'; count: number }
  | { kind: 'busy'; count: number }
  | { kind: 'error'; count: number }
  | { kind: 'pending'; count: number };

export function rollupGroupStatus(
  sessions: readonly Session[],
  effectiveStatusOf: (s: Session) => Session['status'] = (s) => s.status,
): GroupAggregate {
  let pending = 0;
  let error = 0;
  let busy = 0;
  let waiting = 0;
  for (const s of sessions) {
    if (s.pendingPermission || s.pendingQuestion) {
      pending += 1;
      continue;
    }
    const status = effectiveStatusOf(s);
    if (status === 'error' && !s.seen) {
      error += 1;
      continue;
    }
    if (status === 'busy') {
      busy += 1;
      continue;
    }
    if (status === 'waiting' && !s.seen) {
      waiting += 1;
    }
  }
  if (pending > 0) return { kind: 'pending', count: pending };
  if (error > 0) return { kind: 'error', count: error };
  if (busy > 0) return { kind: 'busy', count: busy };
  if (waiting > 0) return { kind: 'waiting', count: waiting };
  return { kind: 'none' };
}
