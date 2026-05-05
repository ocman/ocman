import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import type { Session } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { filterVisibleSessions } from '../../lib/sessionVisibility';
import { computeSidebarHash } from '../../lib/sidebarHelpers';
import { projectRootForDirectory } from '../../lib/worktrees';

const RECENT_SESSIONS_LIMIT = 15;
const SIDEBAR_RECENT_HOURS = 72;
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
  /** Abort signal that fires on session change / unmount. */
  abortSignalRef: MutableRefObject<AbortController | null>;
  /** Navigate handler, used after archiving the current session.
   *  The page passes react-router-dom's `useNavigate()` directly. */
  navigate: (to: string) => void;
}

export interface UseSidebarSessionsResult {
  recentSessions: Session[];
  setRecentSessions: Dispatch<SetStateAction<Session[]>>;
  /** Stable ref-mirror so palette commands can read it without re-registering. */
  recentSessionsRef: MutableRefObject<Session[]>;
  loadingRecentSessions: boolean;
  archivingSessionIds: Set<string>;
  showArchivedRecent: boolean;
  setShowArchivedRecent: Dispatch<SetStateAction<boolean>>;
  /** Ref-mirror of `showArchivedRecent` for the polling closure. */
  showArchivedRecentRef: MutableRefObject<boolean>;
  /** Hash of the most recently displayed sidebar list. The page's
   *  cross-cutting effects (status mirror, permission mirror, seen
   *  mirror) update this so the next poll diffs correctly. */
  lastSiblingsHashRef: MutableRefObject<string>;
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
 *     3 s while the tab is visible;
 *   - the archived-session toggle and its ref-mirror;
 *   - the optimistic archive flow (delayed 220 ms so the fade-out
 *     animation can play) plus the in-flight ids set used by the
 *     renderer to fade rows out;
 *   - the optimistic pin toggle with revert-on-failure;
 *   - the bucketed project groups + per-group status rollup, with
 *     the page session's optimisticStatus overlaid;
 *   - the collapsed-projects set, with the current session's group
 *     forcibly expanded.
 *
 * Cross-cutting writes (status mirror, permission/question mirror,
 * seen mirror, SSE-derived sidebar updates) stay in the page; the
 * hook exposes setRecentSessions / lastSiblingsHashRef so the page
 * can keep them in sync without owning them itself.
 */
export function useSidebarSessions({
  id,
  sessionId,
  collapsedProjects,
  abortSignalRef,
  navigate,
}: UseSidebarSessionsOptions): UseSidebarSessionsResult {
  const getSessions = useApiStore((s) => s.getSessions);
  const archiveSession = useApiStore((s) => s.archiveSession);
  const pinSession = useApiStore((s) => s.pinSession);

  const [recentSessions, setRecentSessions] = useState<Session[]>([]);
  const [loadingRecentSessions, setLoadingRecentSessions] = useState(true);
  const [archivingSessionIds, setArchivingSessionIds] = useState<Set<string>>(new Set());
  const [showArchivedRecent, setShowArchivedRecent] = useState(false);

  const lastSiblingsHashRef = useRef<string>('');
  const archiveTimeoutsRef = useRef<Record<string, number>>({});
  const showArchivedRecentRef = useRef(showArchivedRecent);

  const recentSessionsRef = useRef<Session[]>([]);
  useEffect(() => {
    recentSessionsRef.current = recentSessions;
  }, [recentSessions]);

  const loadRecentSessions = useCallback(async (signal?: AbortSignal) => {
    try {
      const since = Date.now() - SIDEBAR_RECENT_HOURS * 60 * 60 * 1000;
      // /api/sessions can serialize a Go nil slice as JSON `null`;
      // coerce here so .find() / filterVisibleSessions never see null.
      const result = (await getSessions({ since, limit: RECENT_SESSIONS_LIMIT + 5 }, signal)) ?? [];
      if (signal?.aborted) return;
      const visible = (showArchivedRecentRef.current ? result : filterVisibleSessions(result))
        .slice(0, RECENT_SESSIONS_LIMIT);
      const current = result.find((s) => s.id === id);
      const nextRecentSessions = current && !visible.some((s) => s.id === current.id)
        ? [current, ...visible].slice(0, RECENT_SESSIONS_LIMIT)
        : visible;
      const hash = computeSidebarHash(nextRecentSessions);
      if (hash !== lastSiblingsHashRef.current) {
        lastSiblingsHashRef.current = hash;
        setRecentSessions(nextRecentSessions);
      }
      setLoadingRecentSessions(false);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      throw e;
    }
  }, [getSessions, id]);

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
          .catch((err) => console.error('Failed to refresh recent sessions', err));
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
          .catch((err) => console.error('Failed to refresh recent sessions', err));
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
    // Capture the current sibling list and the archived session's
    // position synchronously at click time. Picks the session at
    // `idx + 1` (directly below), or `idx - 1` (directly above) if
    // there's nothing below.
    const isCurrent = target.id === id;
    const idx = recentSessions.findIndex((s) => s.id === target.id);
    const nextSession = isCurrent
      ? (recentSessions[idx + 1] ?? recentSessions[idx - 1])
      : undefined;
    setArchivingSessionIds((prev) => new Set(prev).add(target.id));
    archiveTimeoutsRef.current[target.id] = window.setTimeout(() => {
      archiveSession(target.platform, target.id, target.timeUpdated, true)
        .then(() => {
          setRecentSessions((prev) => showArchivedRecent
            ? prev.map((session) => (session.id === target.id ? { ...session, archived: true } : session))
            : prev.filter((session) => session.id !== target.id));
          if (isCurrent) {
            navigate(nextSession ? `/session/${nextSession.id}` : '/');
          }
        })
        .catch((err) => {
          console.error('Failed to archive session', err);
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
  }, [archiveSession, archivingSessionIds, id, navigate, recentSessions, showArchivedRecent]);

  const handlePinSession = useCallback((e: React.MouseEvent, target: Session) => {
    e.stopPropagation();
    const nextPinned = !target.pinned;
    // Optimistic update — flip the pin in place immediately so the
    // sort settles without waiting for the server.
    setRecentSessions((prev) => prev.map((s) =>
      s.id === target.id
        ? { ...s, pinned: nextPinned, pinnedAt: nextPinned ? Date.now() : 0 }
        : s,
    ));
    pinSession(target.platform, target.id, nextPinned).catch((err) => {
      console.error('Failed to pin/unpin session', err);
      // Revert on failure.
      setRecentSessions((prev) => prev.map((s) =>
        s.id === target.id
          ? { ...s, pinned: target.pinned, pinnedAt: target.pinnedAt }
          : s,
      ));
    });
  }, [pinSession]);

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
    setRecentSessions,
    recentSessionsRef,
    loadingRecentSessions,
    archivingSessionIds,
    showArchivedRecent,
    setShowArchivedRecent,
    showArchivedRecentRef,
    lastSiblingsHashRef,
    loadRecentSessions,
    handleArchiveSession,
    handlePinSession,
    collapsedProjectSet,
  };
}
