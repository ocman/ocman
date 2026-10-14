import { useQuery } from '@tanstack/react-query';
import { api, type Routine } from './api';
import type { WebhookInbox } from './api.types';

/** How often the Routines page refreshes run state while visible. */
export const ROUTINES_POLL_MS = 5000;

export function useRoutines() {
  return useQuery<Routine[]>({
    queryKey: ['routines'],
    queryFn: ({ signal }) => api.routines.list(signal),
    refetchInterval: ROUTINES_POLL_MS,
    // Run state changes constantly: refetch on every return to the tab.
    staleTime: 0,
  });
}

export function useWebhookInboxes() {
  return useQuery<WebhookInbox[]>({
    queryKey: ['webhook-inboxes'],
    queryFn: ({ signal }) => api.webhookInboxes.list(signal),
    refetchInterval: ROUTINES_POLL_MS,
    staleTime: 0,
  });
}
