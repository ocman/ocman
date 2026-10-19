import { Navigate } from 'react-router-dom';
import { InlineAlert } from '../components/InlineAlert';
import { useSessions } from '../lib/queries';
import { useUiStore } from '../lib/uiStore';
import { useIsPhone } from '../lib/useIsPhone';
import { useSidebarSessionList } from '../lib/sessionsCache';
import type { Session } from '../lib/api';

export function RootRedirect() {
  const phone = useIsPhone();
  return phone ? <Navigate to="/sessions" replace /> : <RestoreDesktopSession />;
}

function RestoreDesktopSession() {
  const cached = useSidebarSessionList();
  const lastOpenedSessionId = useUiStore((s) => s.lastOpenedSessionId);
  const lastOpened = cached.find((session) => session.id === lastOpenedSessionId);
  // Returning Home should not fetch a separate full list just to rediscover
  // the session the user already has in the sidebar.
  if (lastOpened && !lastOpened.archived) return <Navigate to={`/session/${lastOpened.id}`} replace />;
  if (!lastOpenedSessionId || lastOpened?.archived) {
    const target = latestActive(cached);
    if (target) return <Navigate to={`/session/${target.id}`} replace />;
  }
  return <FindDesktopSession lastOpenedSessionId={lastOpenedSessionId} />;
}

function latestActive(sessions: Session[]) {
  return sessions.filter((session) => !session.archived).reduce<Session | undefined>(
    (best, session) => !best || session.timeUpdated > best.timeUpdated ? session : best,
    undefined,
  );
}

function FindDesktopSession({ lastOpenedSessionId }: { lastOpenedSessionId?: string }) {
  const sessionsQ = useSessions();
  if (sessionsQ.isLoading) return null;
  if (sessionsQ.isError) {
    const message = sessionsQ.error instanceof Error ? sessionsQ.error.message : 'Could not load sessions.';
    return <InlineAlert onRetry={() => { void sessionsQ.refetch(); }} retrying={sessionsQ.isFetching}>{message}</InlineAlert>;
  }
  const sessions = sessionsQ.data ?? [];
  const lastOpened = sessions.find((session) => session.id === lastOpenedSessionId && !session.archived);
  const latest = latestActive(sessions);
  const target = lastOpened || latest;
  return <Navigate to={target ? `/session/${target.id}` : '/session/new'} replace />;
}
