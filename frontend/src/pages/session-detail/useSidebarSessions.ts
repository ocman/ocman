import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import type { Session } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { filterVisibleSessions } from '../../lib/sessionVisibility';
import { computeSidebarHash, filterInactiveChildren, mergeSidebarSessions, pickNextSessionAfterArchive, resolveOpenSession } from '../../lib/sidebarHelpers';
import { projectRootForDirectory } from '../../lib/worktrees';
import { remoteLog } from '../../lib/remoteLog';
import { onSessionChanged, onSseConnect } from '../../lib/useGlobalEvents';
import { useActivityScope } from '../../lib/activityScopes';

const RECENT_SESSIONS_LIMIT = 15;
/**
 * Reconciliation backstop for events missed while disconnected. Normal
 * updates arrive through the global SSE stream.
 */
const SIDEBAR_REFRESH_MS = 3 * 60 * 1000;
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
 *   - the list of sessions in the last 72 h, refreshed from global SSE
 *     invalidations with a slow reconciliation poll. It lives in Zustand
 *     (useApiStore.recentSessions) so SSE-derived optimistic writes
 *     from the session-detail page survive navigation; the poll then
 *     merges over them (see mergeSidebarSessions for which fields are
 *     sticky and which the server owns);
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
  useActivityScope('sessions');
  const getSessions = useApiStore((s) => s.getSessions);
  const getSession = useApiStore((s) => s.getSession);
  const archiveSession = useApiStore((s) => s.archiveSession);
  const pinSession = useApiStore((s) => s.pinSession);
  const recentSessions = useApiStore((s) => s.recentSessions);
  const storeSetRecentSessions = useApiStore((s) => s.setRecentSessions);
  const patchRecentSession = useApiStore((s) => s.patchRecentSession);
  const sidebarRecentHours = useUiStore((s) => s.sidebarRecentHours);
  const expandProjects = useUiStore((s) => s.expandProjects);

  // Mirror the configured window into a ref so refresh callbacks read
  // the latest value without re-creating loadRecentSessions.
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

  // Fallback for the open session when it falls outside the recent
  // window / the backend's fetch limit: fetched once by id and cached
  // here so reconciliation doesn't repeatedly hit /api/session. Reset when the
  // active session changes.
  const openSessionFallbackRef = useRef<Session | null>(null);
  useEffect(() => {
    openSessionFallbackRef.current = null;
  }, [id]);

  const loadRecentSessions = useCallback(async (signal?: AbortSignal) => {
    try {
      const since = Date.now() - sidebarRecentHoursRef.current * 60 * 60 * 1000;
      // /api/sessions can serialize a Go nil slice as JSON `null`;
      // coerce here so .find() / filterVisibleSessions never see null.
      const result = (await getSessions({ since, limit: RECENT_SESSIONS_LIMIT + 5 }, signal)) ?? [];
      if (signal?.aborted) return;
      // Child sessions are useful while active; completed output has
      // already bubbled up to the parent.
      const rooted = filterInactiveChildren(result, id);
      const visible = (showArchivedRecentRef.current ? rooted : filterVisibleSessions(rooted))
        .slice(0, RECENT_SESSIONS_LIMIT);
      // The re-inject below only works if the open session is in the
      // windowed fetch. When it isn't (older than the recent window, or
      // ranked past the backend's limit) fetch it once by id so the
      // active session is ALWAYS present in the sidebar.
      const resolved = await resolveOpenSession({
        id,
        fetched: result,
        cached: openSessionFallbackRef.current,
        fetchById: async (sid) => (await getSession(sid, 1, 0, signal)).session,
        onError: (err) => remoteLog.warn('sidebar open-session fallback fetch failed', err),
      });
      if (signal?.aborted) return;
      openSessionFallbackRef.current = resolved.cache;
      const current = resolved.session;
      const nextRecentSessions = current && !visible.some((s) => s.id === current.id)
        ? [current, ...visible].slice(0, RECENT_SESSIONS_LIMIT)
        : visible;

      const merged = mergeSidebarSessions(
        nextRecentSessions,
        useApiStore.getState().recentSessions,
        id,
      );

      const hash = computeSidebarHash(merged);
      storeSetRecentSessions(merged, hash);
      setLoadingRecentSessions(false);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      throw e;
    }
  }, [getSessions, getSession, id, storeSetRecentSessions]);

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

  // SSE is the primary invalidation path. Re-fetch after reconnecting to
  // reconcile events missed during the gap.
  useEffect(() => {
    const refresh = () => {
      loadRecentSessions(abortSignalRef.current?.signal)
        .catch((err) => remoteLog.error('Failed to refresh recent sessions', err));
    };
    const unsubscribeChanged = onSessionChanged((sessionID, _session, patch) => {
      if (patch && useApiStore.getState().recentSessions.some((session) => session.id === sessionID)) {
        patchRecentSession(sessionID, patch);
        return;
      }
      refresh();
    });
    const unsubscribeConnect = onSseConnect(refresh);
    return () => {
      unsubscribeChanged();
      unsubscribeConnect();
    };
  }, [loadRecentSessions, abortSignalRef, patchRecentSession]);

  // Slow reconciliation loop, paused while the tab is hidden.
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
            // No session left: stay on the detail page with the `new`
            // sentinel so the sidebar + empty-detail hint show instead
            // of bouncing to the dashboard.
            navigate(nextSession ? `/session/${nextSession.id}` : '/session/new');
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

  // Opening a session expands its project group for good. This used to be
  // derived per-render and never persisted, so navigating to another
  // project re-collapsed the group and the session the user had just been
  // working in vanished from the sidebar (it survived a reload, because the
  // collapse is persisted). Expanding once per opened session keeps
  // collapsing a deliberate user action: it is not re-applied on later
  // sidebar updates, so the user can still collapse the project they are in.
  const expandedForRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (!id || expandedForRef.current === id) return;
    const currentDir = recentSessions.find((s) => s.id === id)?.directory;
    // The list may not have loaded yet; retry when it changes.
    if (!currentDir) return;
    expandedForRef.current = id;
    // Both keys: the raw directory (legacy entries persisted before the
    // worktree fold) and the folded project root.
    expandProjects([currentDir, projectRootForDirectory(currentDir)]);
  }, [id, recentSessions, expandProjects]);

  // Collapsed state as a Set for O(1) membership checks in render.
  const collapsedProjectSet = useMemo(
    () => new Set(collapsedProjects),
    [collapsedProjects],
  );

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
