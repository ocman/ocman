import { Navigate } from 'react-router-dom';
import { InlineAlert } from '../components/InlineAlert';
import { useSessions } from '../lib/queries';
import { useUiStore } from '../lib/uiStore';

export function RootRedirect() {
  const sessionsQ = useSessions();
  const lastOpenedSessionId = useUiStore((s) => s.lastOpenedSessionId);
  if (sessionsQ.isLoading) return null;
  if (sessionsQ.isError) {
    const message = sessionsQ.error instanceof Error ? sessionsQ.error.message : 'Could not load sessions.';
    return <InlineAlert onRetry={() => { void sessionsQ.refetch(); }} retrying={sessionsQ.isFetching}>{message}</InlineAlert>;
  }
  const active = (sessionsQ.data ?? []).filter((session) => !session.archived);
  const lastOpened = active.find((session) => session.id === lastOpenedSessionId);
  const latest = active.reduce<(typeof active)[number] | undefined>(
    (best, session) => !best || session.timeUpdated > best.timeUpdated ? session : best,
    undefined,
  );
  const target = lastOpened || latest;
  return <Navigate to={target ? `/session/${target.id}` : '/session/new'} replace />;
}
