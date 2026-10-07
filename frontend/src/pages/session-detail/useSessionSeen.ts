import { useCallback, useEffect, useRef, useState } from 'react';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { cleanTitle, shortPath } from '../../lib/format';
import { useHeaderInfo, usePageTitle } from '../../lib/headerContext';
import { recheckFaviconNotify } from '../../lib/useFaviconNotify';
import { onSessionChanged } from '../../lib/useGlobalEvents';
import { remoteLog } from '../../lib/remoteLog';
import { projectRootForDirectory } from '../../lib/worktrees';
import type { SessionMetadata } from '../../lib/sessionReducer';

export interface UseSessionSeenOptions {
  session: SessionMetadata | null;
  patchSession: (patch: Partial<SessionMetadata>) => void;
}

/**
 * Per-session-open bookkeeping: mark the session seen (server + both
 * local copies), record it as recently opened, and publish the header
 * info + document title.
 */
export function useSessionSeen({ session, patchSession }: UseSessionSeenOptions): void {
  const [visible, setVisible] = useState(() => !document.hidden);
  useEffect(() => {
    const onVisibility = () => setVisible(!document.hidden);
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, []);
  const recordOpenedSession = useUiStore((state) => state.recordOpenedSession);
  const markSessionSeen = useApiStore((state) => state.markSessionSeen);
  const patchRecentSession = useApiStore((state) => state.patchRecentSession);
  const { setInfo } = useHeaderInfo();
  usePageTitle(cleanTitle(session?.title) || 'Session');

  // Only navigation clears the archive. Visibility and read-watermark
  // updates must preserve an archive made in another tab.
  const sessionSeenId = session?.id;
  const sessionSeenPlatform = session?.platform;
  const sessionSeenUpdated = session?.timeUpdated || 0;
  const sessionSeenInterrupted = session?.status === 'interrupted';
  const lastMarked = useRef(0);
  const openedIdentity = useRef('');
  const pendingMark = useRef<(() => void) | null>(null);
  const markSeen = useCallback((platform: string, id: string, updated: number, opening = false) => {
    if (document.hidden) return;
    lastMarked.current = updated;
    patchRecentSession(id, { seen: true, seenTimeUpdated: updated, ...(opening ? { archived: false } : {}) });
    const request = sessionSeenInterrupted
      ? markSessionSeen(platform, id, updated, true)
      : markSessionSeen(platform, id, updated);
    void request
      .then(() => {
        recheckFaviconNotify();
      })
      .catch((err) => remoteLog.error('Failed to mark session seen', err));
  }, [markSessionSeen, patchRecentSession, sessionSeenInterrupted]);

  useEffect(() => {
    if (!visible || !sessionSeenId || !sessionSeenPlatform) return;
    const identity = `${sessionSeenPlatform}\0${sessionSeenId}`;
    const opening = openedIdentity.current !== identity;
    openedIdentity.current = identity;
    recordOpenedSession(sessionSeenId);
    patchSession({ seen: true, ...(opening ? { archived: false } : {}) });
    markSeen(sessionSeenPlatform, sessionSeenId, sessionSeenUpdated, opening);
    return () => {
      // A quick departure still acknowledges the latest content actually shown.
      pendingMark.current?.();
      pendingMark.current = null;
    };
  // Entry bookkeeping runs once per identity, not on every streamed update.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionSeenId, sessionSeenPlatform, sessionSeenInterrupted, visible]);

  // The entry can come from cache. Coalesce newer authoritative/streamed
  // timestamps so its stale watermark does not leave the open session unread.
  useEffect(() => {
    if (!visible || !sessionSeenId || !sessionSeenPlatform || sessionSeenUpdated <= lastMarked.current) return;
    const mark = () => {
      markSeen(sessionSeenPlatform, sessionSeenId, sessionSeenUpdated);
      pendingMark.current = null;
    };
    pendingMark.current = mark;
    const timer = setTimeout(() => {
      if (document.hidden) return;
      mark();
      patchSession({ seen: true });
    }, 500);
    return () => clearTimeout(timer);
  }, [sessionSeenId, sessionSeenPlatform, sessionSeenUpdated, markSeen, patchSession, visible]);

  // Upstream renames (OpenCode auto-title, TUI /rename, another tab). For a
  // slow client the hub merges patches, and collapses anything involving an
  // identity-only change into one identity-only event, so re-read the title
  // on those. Status-only patches never hide a title and are ignored.
  const peekSession = useApiStore((state) => state.peekSession);
  useEffect(() => {
    if (!sessionSeenId) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    // Bumped by every title event and every fetch; a response applies only
    // if nothing newer happened while it was in flight.
    let revision = 0;
    const controller = new AbortController();
    const unsubscribe = onSessionChanged((changedId, _session, patch) => {
      if (changedId !== sessionSeenId) return;
      if (patch?.title) {
        revision++;
        patchSession({ title: patch.title });
        return;
      }
      if (patch || timer !== undefined) return;
      timer = setTimeout(() => {
        timer = undefined;
        const requested = ++revision;
        peekSession(sessionSeenId, controller.signal)
          .then(({ session: row }) => { if (row.title && requested === revision) patchSession({ title: row.title }); })
          .catch((err) => { if (!controller.signal.aborted) remoteLog.error('Failed to refresh session title', err); });
      }, 250);
    });
    return () => {
      unsubscribe();
      clearTimeout(timer);
      controller.abort();
    };
  }, [sessionSeenId, patchSession, peekSession]);

  // Header info.
  useEffect(() => {
    if (!session) return;
    const s = session;
    setInfo({
      sessionId: s.id,
      sessionTitle: cleanTitle(s.title) || 'Untitled',
      sessionPlatform: s.platform,
      sessionProject: shortPath(projectRootForDirectory(s.directory)),
      sessionProjectFull: s.directory,
      sessionRemoteId: s.remoteId,
      sessionRemoteName: s.remoteName,
      sessionRemoteStale: s.stale,
    });
    return () => setInfo({});
  }, [session, setInfo]);
}
