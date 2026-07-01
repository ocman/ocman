import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import type { Session } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { filterVisibleSessions } from '../../lib/sessionVisibility';
import { computeSidebarHash, filterOrphanChildren, pickNextSessionAfterArchive } from '../../lib/sidebarHelpers';
import { projectRootForDirectory } from '../../lib/worktrees';
import { remoteLog } from '../../lib/remoteLog';

const RECENT_SESSIONS_LIMIT = 15;
/**
 * How often the Recent Sessions sidebar re-polls /api/sessions. Kept
 * low enough to feel live, but not so low that we hammer the OpenCode
 * port-discovery + per-instance HTTP fan-out on every tick. Polling
 * is paused while the tab is hidden.
 */
const SIDEBAR_REFRESH_MS = 3000;
/**
 * How long to delay archive completion so the row's fade-out
 * animation can finish. Matches the CSS transition.
 */
const ARCHIVE_ANIMATION_MS = 220;

export interface UseSidebarSessionsOptions {
  /** The active session id from the URL. */
  id: string | undefined;
  /** Resolved session id once the page has loaded the detail. */
  sessionId: string | undefined;
  /** Persisted collapsed project keys. */
  collapsedProjects: Iterable<string>;
  /** Current sidebar view. When 'projects', archiving the active
   *  session navigates to the most recent sibling in the same project
   *  rather than the adjacent row in the flat list. */
  sidebarView: 'recent' | 'projects';
  /** Abort signal that fires on session change / unmount. */
  abortSignalRef: MutableRefObject<AbortController | null>;
  /** Navigate handler, used after archiving the current session.
   *  The page passes react-router-dom's `useNavigate()` directly. */
  navigate: (to: string) => void;
}

export interface UseSidebarSessionsResult {
  recentSessions: Session[];
  /** Stable ref-mirror so palette commands can read it without re-registering. */
  recentSessionsRef: MutableRefObject<Session[]>;
  loadingRecentSessions: boolean;
  archivingSessionIds: Set<string>;
  showArchivedRecent: boolean;
  setShowArchivedRecent: Dispatch<SetStateAction<boolean>>;
  /** Ref-mirror of `showArchivedRecent` for the polling closure. */
  showArchivedRecentRef: MutableRefObject<boolean>;
  loadRecentSessions: (signal?: AbortSignal) => Promise<void>;
  handleArchiveSession: (e: React.MouseEvent, target: Session) => void;
  handlePinSession: (e: React.MouseEvent, target: Session) => void;
  /** Set of collapsed project keys with the current session's group
   *  forcibly removed so the user always sees where they are. */
  collapsedProjectSet: Set<string>;
}

/**
 * Owns everything the Recent Sessions sidebar needs:
 *
 *   - the polled list of sessions in the last 72 h, refreshed every
 *     3 s while the tab is visible. The list lives in Zustand
 *     (useApiStore.recentSessions) so SSE-derived optimistic writes
 *     from the session-detail page survive navigation and are never
 *     clobbered by the poll (last write wins);
 *   - the archived-session toggle and its ref-mirror;
 *   - the optimistic archive flow (delayed 220 ms so the fade-out
 *     animation can play) plus the in-flight ids set used by the
 *     renderer to fade rows out;
 *   - the optimistic pin toggle with revert-on-failure;
 *   - the collapsed-projects set, with the current session's group
 *     forcibly expanded.
 *
 * Cross-cutting writes (status mirror, permission/question mirror,
 * seen mirror) go directly to the store via patchRecentSession so
 * they win over any concurrent poll replace.
 */
export function useSidebarSessions({
  id,
  sessionId,
  collapsedProjects,
  sidebarView,
  abortSignalRef,
  navigate,
}: UseSidebarSessionsOptions): UseSidebarSessionsResult {
  const getSessions = useApiStore((s) => s.getSessions);
  const archiveSession = useApiStore((s) => s.archiveSession);
  const pinSession = useApiStore((s) => s.pinSession);
  const recentSessions = useApiStore((s) => s.recentSessions);
  const storeSetRecentSessions = useApiStore((s) => s.setRecentSessions);
  const patchRecentSession = useApiStore((s) => s.patchRecentSession);
  const sidebarRecentHours = useUiStore((s) => s.sidebarRecentHours);

  // Mirror the configured window into a ref so the polling closure
  // reads the latest value without re-creating loadRecentSessions
  // (which would restart the 3 s poll interval on every settings edit).
  const sidebarRecentHoursRef = useRef(sidebarRecentHours);
  useEffect(() => {
    sidebarRecentHoursRef.current = sidebarRecentHours;
  }, [sidebarRecentHours]);

  const [loadingRecentSessions, setLoadingRecentSessions] = useState(true);
  const [archivingSessionIds, setArchivingSessionIds] = useState<Set<string>>(new Set());
  const [showArchivedRecent, setShowArchivedRecent] = useState(false);

  const archiveTimeoutsRef = useRef<Record<string, number>>({});
  const showArchivedRecentRef = useRef(showArchivedRecent);

  const recentSessionsRef = useRef<Session[]>([]);
  useEffect(() => {
    recentSessionsRef.current = recentSessions;
  }, [recentSessions]);

  const loadRecentSessions = useCallback(async (signal?: AbortSignal) => {
    try {
      const since = Date.now() - sidebarRecentHoursRef.current * 60 * 60 * 1000;
      // /api/sessions can serialize a Go nil slice as JSON `null`;
      // coerce here so .find() / filterVisibleSessions never see null.
      const result = (await getSessions({ since, limit: RECENT_SESSIONS_LIMIT + 5 }, signal)) ?? [];
      if (signal?.aborted) return;
      // Drop orphan subagent/child sessions so they aren't promoted to
      // standalone top-level rows (e.g. "... (@explore subagent)").
      const rooted = filterOrphanChildren(result, id);
      const visible = (showArchivedRecentRef.current ? rooted : filterVisibleSessions(rooted))
        .slice(0, RECENT_SESSIONS_LIMIT);
      const current = result.find((s) => s.id === id);
      const nextRecentSessions = current && !visible.some((s) => s.id === current.id)
        ? [current, ...visible].slice(0, RECENT_SESSIONS_LIMIT)
        : visible;

      // The poll is the authoritative source for the full list, but
      // optimistic writes (status, seen, pendingPermission/Question) made
      // via patchRecentSession may have arrived since the last poll.
      // Preserve those fields so a stale server response doesn't clobber them.
      const currentStore = useApiStore.getState().recentSessions;
      const merged = nextRecentSessions.map((s) => {
        // The active session is always unarchived on open (handleSession
        // unarchives it server-side). Force the flag off so a poll that
        // raced the server-side unarchive can't show it as archived —
        // this holds even on the very first poll (empty store, no `live`).
        const unarchived = s.id === id ? { ...s, archived: false } : s;
        const live = currentStore.find((ls) => ls.id === s.id);
        if (!live) return unarchived;
        // Prefer the more-recent status: if the store has 'busy' and the
        // server still shows a stale status, keep 'busy'. In all other
        // cases the poll wins (it is the source of truth for terminal states).
        const status = live.status === 'busy' && s.status !== 'busy' ? 'busy' : s.status;
        return {
          ...unarchived,
          status,
          // Preserve seen/pending flags that the SSE may have set more recently.
          seen: live.seen || s.seen,
          pendingPermission: live.pendingPermission || s.pendingPermission,
          pendingQuestion: live.pendingQuestion || s.pendingQuestion,
        };
      });

      const hash = computeSidebarHash(merged);
      storeSetRecentSessions(merged, hash);
      setLoadingRecentSessions(false);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      throw e;
    }
  }, [getSessions, id, storeSetRecentSessions]);

  // Initial load when the active session changes (or is set the
  // first time).
  useEffect(() => {
    if (!sessionId) return;
    void loadRecentSessions(abortSignalRef.current?.signal);
    // abortSignalRef is intentionally read at call-time, not as a dep.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, loadRecentSessions]);

  // Re-fetch when the user toggles the archived view (skip the very
  // first render — the sessionId effect above already loaded once).
  const showArchivedRecentMounted = useRef(true);
  useEffect(() => {
    showArchivedRecentRef.current = showArchivedRecent;
    if (showArchivedRecentMounted.current) {
      showArchivedRecentMounted.current = false;
      return;
    }
    void loadRecentSessions(abortSignalRef.current?.signal);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [showArchivedRecent, loadRecentSessions]);

  // Polling loop, paused while the tab is hidden.
  useEffect(() => {
    let refreshId: number | null = null;
    const start = () => {
      if (refreshId !== null) return;
      refreshId = window.setInterval(() => {
        loadRecentSessions(abortSignalRef.current?.signal)
          .catch((err) => remoteLog.error('Failed to refresh recent sessions', err));
      }, SIDEBAR_REFRESH_MS);
    };
    const stop = () => {
      if (refreshId === null) return;
      window.clearInterval(refreshId);
      refreshId = null;
    };
    const onVisibility = () => {
      if (document.hidden) {
        stop();
      } else {
        // Fire once immediately on re-focus so the user sees fresh
        // data without waiting a full interval, then resume polling.
        loadRecentSessions(abortSignalRef.current?.signal)
          .catch((err) => remoteLog.error('Failed to refresh recent sessions', err));
        start();
      }
    };
    if (!document.hidden) start();
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      stop();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadRecentSessions]);

  // Cleanup any outstanding archive timers on unmount.
  useEffect(() => () => {
    Object.values(archiveTimeoutsRef.current).forEach((timeoutId) => window.clearTimeout(timeoutId));
  }, []);

  const handleArchiveSession = useCallback((e: React.MouseEvent, target: Session) => {
    e.stopPropagation();
    if (archivingSessionIds.has(target.id)) return;
    // Capture the next session to navigate to synchronously at click
    // time. In the flat 'recent' view we pick the adjacent row; in the
    // grouped 'projects' view we stay within the same project (most
    // recent remaining sibling) so closing a session keeps the user
    // where they were working. See pickNextSessionAfterArchive.
    const isCurrent = target.id === id;
    const nextSession = isCurrent
      ? pickNextSessionAfterArchive(recentSessions, target.id, sidebarView)
      : undefined;
    setArchivingSessionIds((prev) => new Set(prev).add(target.id));
    archiveTimeoutsRef.current[target.id] = window.setTimeout(() => {
      archiveSession(target.platform, target.id, target.timeUpdated, true)
        .then(() => {
          // Remember the just-closed session so it can be reopened via the
          // Alt+Shift+N "reopen last closed session" shortcut.
          useApiStore.getState().pushClosedSession({
            platform: target.platform,
            id: target.id,
            timeUpdated: target.timeUpdated,
          });
          const { recentSessions: current, setRecentSessions: storeSetter, recentSessionsHash } = useApiStore.getState();
          const next = showArchivedRecentRef.current
            ? current.map((session) => (session.id === target.id ? { ...session, archived: true } : session))
            : current.filter((session) => session.id !== target.id);
          // Only write if something actually changed.
          if (next !== current) storeSetter(next, computeSidebarHash(next));
          // Suppress TS: recentSessionsHash is read to satisfy the linter,
          // but we don't need it here — the store handles dedup internally.
          void recentSessionsHash;
          if (isCurrent) {
            navigate(nextSession ? `/session/${nextSession.id}` : '/');
          }
        })
        .catch((err) => {
          remoteLog.error('Failed to archive session', err);
        })
        .finally(() => {
          setArchivingSessionIds((prev) => {
            const next = new Set(prev);
            next.delete(target.id);
            return next;
          });
          delete archiveTimeoutsRef.current[target.id];
        });
    }, ARCHIVE_ANIMATION_MS);
  }, [archiveSession, archivingSessionIds, id, navigate, recentSessions, sidebarView]);

  const handlePinSession = useCallback((e: React.MouseEvent, target: Session) => {
    e.stopPropagation();
    const nextPinned = !target.pinned;
    // Optimistic update — flip the pin in place immediately so the
    // sort settles without waiting for the server.
    patchRecentSession(target.id, { pinned: nextPinned, pinnedAt: nextPinned ? Date.now() : 0 });
    pinSession(target.platform, target.id, nextPinned).catch((err) => {
      remoteLog.error('Failed to pin/unpin session', err);
      // Revert on failure.
      patchRecentSession(target.id, { pinned: target.pinned, pinnedAt: target.pinnedAt });
    });
  }, [pinSession, patchRecentSession]);

  // Collapsed state as a Set for O(1) membership checks in render.
  // The current session's group is force-expanded regardless of
  // persisted state so the user can always see where they are.
  const collapsedProjectSet = useMemo(() => {
    const set = new Set(collapsedProjects);
    const currentDir = recentSessions.find((s) => s.id === id)?.directory;
    if (currentDir) {
      set.delete(currentDir); // legacy keys persisted before fold
      set.delete(projectRootForDirectory(currentDir));
    }
    return set;
  }, [collapsedProjects, recentSessions, id]);

  return {
    recentSessions,
    recentSessionsRef,
    loadingRecentSessions,
    archivingSessionIds,
    showArchivedRecent,
    setShowArchivedRecent,
    showArchivedRecentRef,
    loadRecentSessions,
    handleArchiveSession,
    handlePinSession,
    collapsedProjectSet,
  };
}
