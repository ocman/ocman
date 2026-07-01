import type { Session } from './api';
import { projectRootForDirectory } from './worktrees';

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
        `${s.id}|${s.status}|${s.timeUpdated}|${s.pendingPermission ? 'p' : ''}${s.pendingQuestion ? 'q' : ''}${s.notice ? `|n:${s.notice.kind}:${s.notice.retryAt}:${s.notice.attempt}` : ''}`,
    )
    .join(',');
}

/**
 * Drop orphan subagent/child sessions from a flat sidebar list.
 *
 * A child (one with a `parentId`) only belongs nested under its parent.
 * When the parent is outside the fetched window, `nestSessions` would
 * promote the orphan to a standalone top-level row (e.g.
 * "... (@explore subagent)"). We keep a child only when its parent is
 * present in the same list, or when it is the currently-open session.
 */
export function filterOrphanChildren(
  sessions: readonly Session[],
  currentId?: string,
): Session[] {
  const presentIds = new Set(sessions.map((s) => s.id));
  return sessions.filter(
    (s) => !s.parentId || s.id === currentId || presentIds.has(s.parentId),
  );
}

/**
 * Pick the session to navigate to after archiving the active session
 * from the sidebar.
 *
 *   - In the flat 'recent' view we move to the row directly below the
 *     archived session (`idx + 1`), falling back to the row directly
 *     above (`idx - 1`) when the archived session was last in the list.
 *   - In the grouped 'projects' view we stay within the same project:
 *     the most recently updated remaining session whose project root
 *     matches the archived session's. If the project has no other
 *     sessions we return undefined (the caller navigates home).
 *
 * Returns `undefined` when there is no suitable next session, in which
 * case the caller should navigate to the dashboard.
 */
export function pickNextSessionAfterArchive(
  sessions: readonly Session[],
  targetId: string,
  view: 'recent' | 'projects',
): Session | undefined {
  if (view === 'projects') {
    const target = sessions.find((s) => s.id === targetId);
    if (!target) return undefined;
    const targetRoot = projectRootForDirectory(target.directory || '');
    return sessions
      .filter(
        (s) =>
          s.id !== targetId &&
          projectRootForDirectory(s.directory || '') === targetRoot,
      )
      .sort((a, b) => b.timeUpdated - a.timeUpdated)[0];
  }
  const idx = sessions.findIndex((s) => s.id === targetId);
  if (idx < 0) return undefined;
  return sessions[idx + 1] ?? sessions[idx - 1];
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
