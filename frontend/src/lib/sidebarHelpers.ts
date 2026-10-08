import type { Session } from './api';
import { projectRootForDirectory } from './worktrees';

// Throttle streaming activity updates to the relative-time label's precision.
export function compareSidebarActivity(a: Pick<Session, 'timeUpdated'>, b: Pick<Session, 'timeUpdated'>): number {
  return Math.floor(b.timeUpdated / 60_000) - Math.floor(a.timeUpdated / 60_000);
}

/** Streaming and read-state changes leave the ordering key unchanged. */
export function compareSidebarCompletion(a: Session, b: Session): number {
  return (b.lastTurnCompletedAt || b.timeCreated || 0) - (a.lastTurnCompletedAt || a.timeCreated || 0)
    || `${a.platform}:${a.id}`.localeCompare(`${b.platform}:${b.id}`);
}

/**
 * Compute a cheap dedup hash for a sidebar session list. Used to skip
 * `setState` calls when a poll returned identical data — the
 * SessionDetail page rebuilds its memoised project groups whenever
 * `recentSessions` changes, so avoiding spurious re-renders matters.
 *
 * Includes status, activity, prompt flags, notices and read metadata so
 * an unread correction is applied even when SSE already updated activity.
 */
export function computeSidebarHash(sessions: readonly Session[]): string {
  return sessions
    .map(
      (s) =>
        `${s.id}|${s.status}|${s.timeUpdated}|${s.lastTurnCompletedAt ?? 0}|${s.pendingPermission ? 'p' : ''}${s.pendingQuestion ? 'q' : ''}${s.notice ? `|n:${s.notice.kind}:${s.notice.retryAt}:${s.notice.attempt}` : ''}|${s.seen}|${s.seenTimeUpdated}|${s.unreadCount}|${s.archived}${s.factoryAttemptId ? `|f:${s.factoryAttemptId}` : ''}`,
    )
    .join(',');
}

/**
 * Drop inactive subagent/child sessions from a flat sidebar list.
 *
 * A child remains useful while it is busy or waiting for input. Once it
 * completes, its result has bubbled up to the parent and it only adds
 * noise. The currently-open child remains visible so its sidebar row
 * does not disappear while the user is viewing it.
 */
export function filterInactiveChildren(
  sessions: readonly Session[],
  currentId?: string,
): Session[] {
  const isActive = (s: Session) =>
    s.status === 'busy' || s.pendingPermission || s.pendingQuestion;
  return sessions.filter((s) => {
    if (s.id === currentId || s.pinned) return true;
    // Match the backend list's internal-session exclusion. SSE can fetch
    // details directly, which also exposes parentless auto-approve judges.
    if (!s.parentId && s.title?.endsWith(' subagent)')) return false;
    return !s.parentId || isActive(s);
  });
}

/**
 * Resolve the currently-open session so it is ALWAYS available to the
 * sidebar, even when it falls outside the recent-window fetch or is
 * ranked past the backend's row limit.
 *
 * Lookup order:
 *   1. the windowed poll result (`fetched`);
 *   2. a previously cached fallback (`cached`), so we hit the network
 *      at most once per open session;
 *   3. a one-shot fetch by id (`fetchById`).
 *
 * Returns the resolved session (or undefined) plus the value to store
 * back into the fallback cache. A failed `fetchById` is non-fatal: it
 * returns `session: undefined` and leaves the cache untouched.
 */
export async function resolveOpenSession(opts: {
  id: string | undefined;
  fetched: readonly Session[];
  cached: Session | null;
  fetchById: (id: string) => Promise<Session>;
  onError?: (err: unknown) => void;
}): Promise<{ session: Session | undefined; cache: Session | null }> {
  const { id, fetched, cached, fetchById, onError } = opts;
  const inList = id ? fetched.find((s) => s.id === id) : undefined;
  if (inList) return { session: inList, cache: cached };
  if (!id || id === 'new') return { session: undefined, cache: cached };
  if (cached) return { session: cached, cache: cached };
  try {
    const session = await fetchById(id);
    return { session, cache: session };
  } catch (err) {
    onError?.(err);
    return { session: undefined, cache: cached };
  }
}

/**
 * Merge a fresh /api/sessions poll result over the current store rows.
 *
 * Read watermarks, activity and completion timestamps are monotonic. A stale response
 * must not undo a local read, but newer activity must become unread again.
 *
 * `status` is not sticky either: sticky-busy used to live here because
 * the server derived status from the last message's shape and could not
 * be trusted to be current; it now reports the agent's own turn state,
 * so overriding it would only keep a finished turn spinning.
 *
 * The pending permission/question flags are deliberately NOT sticky.
 * They used to be merged as `live.pending… || server`, which also
 * preserved values that came from an earlier *poll*, so a badge lit by
 * the backend could never go out again once the prompt was answered.
 * The optimistic write still lights the badge instantly; the next poll
 * (≤3 s) is what turns it off.
 *
 * `activeId` is forced unarchived: opening a session unarchives it
 * server-side, so a poll that raced that write must not show it
 * archived.
 */
export function mergeSidebarSessions(
  next: readonly Session[],
  current: readonly Session[],
  activeId?: string,
  requestStart?: readonly Session[],
  pendingReads?: Readonly<Record<string, { timeUpdated: number }>>,
): Session[] {
  return next.map((s) => {
    const unarchived = s.id === activeId ? { ...s, archived: false } : s;
    const live = current.find((ls) => ls.id === s.id && ls.platform === s.platform);
    const pending = pendingReads?.[`${s.platform}:${s.id}`];
    if (!live) return s.status === 'interrupted' && pending && pending.timeUpdated >= s.timeUpdated
      ? { ...unarchived, seen: true, seenTimeUpdated: Math.max(s.seenTimeUpdated, pending.timeUpdated) }
      : unarchived;
    const readDuringRequest = live.status === 'interrupted' && (
      (pending !== undefined && pending.timeUpdated >= s.timeUpdated)
      || (!!requestStart && live !== requestStart.find((ls) => ls.id === s.id && ls.platform === s.platform))
    );
    return {
      ...unarchived,
      seen: s.seen || ((s.status !== 'interrupted' || readDuringRequest) && live.seen && live.seenTimeUpdated >= s.timeUpdated),
      seenTimeUpdated: Math.max(live.seenTimeUpdated, s.seenTimeUpdated),
      timeUpdated: Math.max(live.timeUpdated, s.timeUpdated),
      lastTurnCompletedAt: Math.max(live.lastTurnCompletedAt ?? 0, s.lastTurnCompletedAt ?? 0),
    };
  }).sort(compareSidebarCompletion);
}

/**
 * Sessions the sidebar currently shows, in rendered order, after every
 * filter (search, children, Factory, routines, archived, collapsed
 * groups). Written by SessionSidebar; null while it is not mounted.
 */
// ponytail: module-level ref, there is one sidebar. Move to a store if a second one appears.
export const visibleSidebarSessions: { current: readonly Session[] | null } = { current: null };

/** Candidates for "the next session": what the sidebar shows, else `fallback`. */
export function sidebarNavigableSessions(fallback: readonly Session[]): readonly Session[] {
  return visibleSidebarSessions.current ?? fallback;
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
 *     sessions we fall back to the most recent remaining session
 *     anywhere (mirroring how `/` opens the latest session).
 *
 * The archived session may be absent from `sessions` (a filter hides it):
 * the flat view then opens the first row and the grouped view still
 * prefers its project. Returns `undefined` when `sessions` holds nothing
 * else, in which case the caller opens an empty conversation.
 */
export function pickNextSessionAfterArchive(
  sessions: readonly Session[],
  target: Pick<Session, 'id' | 'directory'>,
  view: 'recent' | 'projects',
): Session | undefined {
  const others = sessions.filter((s) => s.id !== target.id);
  if (view === 'projects') {
    const newest = (list: Session[]) => list.sort((a, b) => b.timeUpdated - a.timeUpdated)[0];
    const targetRoot = projectRootForDirectory(target.directory || '');
    // Last session in the project: fall back to the newest remaining
    // session anywhere so we open a session instead of the dashboard.
    return newest(others.filter((s) => projectRootForDirectory(s.directory || '') === targetRoot))
      ?? newest(others);
  }
  const idx = sessions.findIndex((s) => s.id === target.id);
  if (idx < 0) return others[0];
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
 *   2. `error`   — any unseen errored or interrupted session.
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
    if ((status === 'error' || status === 'interrupted') && !s.seen) {
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
