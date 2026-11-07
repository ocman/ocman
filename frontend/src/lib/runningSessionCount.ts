import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './api';
import { useActivityScope } from './activityScopes';
import { queryEventRefresh } from './queryEventRefresh';
import { onSessionChanged, onSseConnect } from './useGlobalEvents';

// Kept for callers of the compact endpoint; the current navigation no longer
// mounts this hook. Events are invalidations, not arithmetic count increments.
export function useRunningSessionCount(enabled: boolean) {
  useActivityScope(enabled ? 'sessions' : undefined);
  const client = useQueryClient();
  useEffect(() => {
    if (!enabled) return;
    const refresh = queryEventRefresh(client, ['running-session-count']);
    const stopChanges = onSessionChanged(() => refresh.schedule());
    const stopConnect = onSseConnect(() => refresh.schedule());
    return () => { stopChanges(); stopConnect(); refresh.dispose(); };
  }, [client, enabled]);
  return useQuery({
    queryKey: ['running-session-count'],
    queryFn: ({ signal }) => api.runningSessionCount(signal),
    enabled,
    // Recovery for missed/coalesced events and lease expiry, not the main path.
    refetchInterval: 3 * 60 * 1000,
  });
}
