import { useEffect } from 'react';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { cleanTitle, shortPath } from '../../lib/format';
import { useHeaderInfo, usePageTitle } from '../../lib/headerContext';
import { recheckFaviconNotify } from '../../lib/useFaviconNotify';
import { remoteLog } from '../../lib/remoteLog';
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
  const recordOpenedSession = useUiStore((state) => state.recordOpenedSession);
  const markSessionSeen = useApiStore((state) => state.markSessionSeen);
  const patchRecentSession = useApiStore((state) => state.patchRecentSession);
  const { setInfo } = useHeaderInfo();
  usePageTitle(cleanTitle(session?.title) || 'Session');

  // Mark session as seen on entry. Opening a session also unarchives it
  // server-side (handleSession), so optimistically clear the archived flag
  // in the sidebar row too — otherwise the row stays hidden/greyed until
  // the next /api/sessions poll catches up.
  const sessionSeenId = session?.id;
  const sessionSeenPlatform = session?.platform;
  const sessionSeenUpdated = session?.timeUpdated || 0;
  useEffect(() => {
    if (!sessionSeenId || !sessionSeenPlatform) return;
    recordOpenedSession(sessionSeenId);
    patchSession({ seen: true, archived: false });
    patchRecentSession(sessionSeenId, { seen: true, archived: false });
    void markSessionSeen(sessionSeenPlatform, sessionSeenId, sessionSeenUpdated)
      .then(() => {
        recheckFaviconNotify();
      })
      .catch((err) => remoteLog.error('Failed to mark session seen', err));
  }, [markSessionSeen, sessionSeenId, sessionSeenPlatform, sessionSeenUpdated, patchRecentSession, patchSession, recordOpenedSession]);

  // Header info.
  useEffect(() => {
    if (!session) return;
    const s = session;
    setInfo({
      sessionId: s.id,
      sessionTitle: cleanTitle(s.title) || 'Untitled',
      sessionPlatform: s.platform,
      sessionProject: shortPath(s.directory),
      sessionProjectFull: s.directory,
      sessionRemoteId: s.remoteId,
      sessionRemoteName: s.remoteName,
      sessionRemoteStale: s.stale,
    });
    return () => setInfo({});
  }, [session, setInfo]);
}
