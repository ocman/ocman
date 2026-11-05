import { useEffect } from 'react';
import { onSessionChanged, onSseConnect } from './useGlobalEvents';

/** Reconcile owner-local state on live changes and after missed events. */
export function useSessionLiveRefresh(sessionId: string, platform: string, refresh: () => Promise<unknown>): void {
  useEffect(() => {
    const reload = () => { void refresh().catch(() => {}); };
    const stopChanges = onSessionChanged((id, _session, _patch, owner) => {
      if (id === sessionId && owner === platform) reload();
    });
    const stopConnect = onSseConnect(reload);
    return () => { stopChanges(); stopConnect(); };
  }, [sessionId, platform, refresh]);
}
