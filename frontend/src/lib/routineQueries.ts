import { useQuery } from '@tanstack/react-query';
import { api, type Project, type Routine, type Session } from './api';
import { useProjects, useSessions } from './queries';
import type { WebhookInbox } from './api.types';

/** How often the Routines page refreshes run state while visible. */
export const ROUTINES_POLL_MS = 5000;

function useRoutines() {
  return useQuery<Routine[]>({
    queryKey: ['routines'],
    queryFn: ({ signal }) => api.routines.list(signal),
    refetchInterval: ROUTINES_POLL_MS,
    // Run state changes constantly: refetch on every return to the tab.
    staleTime: 0,
  });
}

function useWebhookInboxes() {
  return useQuery<WebhookInbox[]>({
    queryKey: ['webhook-inboxes'],
    queryFn: ({ signal }) => api.webhookInboxes.list(signal),
    refetchInterval: ROUTINES_POLL_MS,
    staleTime: 0,
  });
}

/** A query or action failure as user-facing text. */
function errorText(error: unknown, fallback: string): string {
  if (!error) return '';
  return error instanceof Error ? error.message : fallback;
}

const EMPTY_ROUTINES: Routine[] = [];
const EMPTY_INBOXES: WebhookInbox[] = [];
const EMPTY_PROJECTS: Project[] = [];
const EMPTY_SESSIONS: Session[] = [];

/** The Routines page's two polled lists, read and reloaded together. */
export function useRoutineLists() {
  const routinesQ = useRoutines();
  const inboxesQ = useWebhookInboxes();
  return {
    routines: routinesQ.data ?? EMPTY_ROUTINES,
    inboxes: inboxesQ.data ?? EMPTY_INBOXES,
    loading: routinesQ.isPending || inboxesQ.isPending,
    // Both lists loaded. Editors read a routine's trigger from the inboxes
    // once, so mounting one before they arrive would save an empty trigger.
    ready: routinesQ.data !== undefined && inboxesQ.data !== undefined,
    loadError: errorText(routinesQ.error ?? inboxesQ.error, 'Could not load routines.'),
    // Bumps on every successful read so an open history drawer refreshes too.
    refreshKey: routinesQ.dataUpdatedAt,
    reload: async () => {
      await Promise.all([routinesQ.refetch({ throwOnError: true }), inboxesQ.refetch({ throwOnError: true })]);
    },
  };
}

/** Projects and the chosen project's sessions for the routine form. */
export function useRoutineFormLookups(directory: string) {
  const projectsQ = useProjects();
  const sessionsQ = useSessions({ dir: directory }, { enabled: !!directory });
  return {
    projects: projectsQ.data ?? EMPTY_PROJECTS,
    sessions: (directory && sessionsQ.data) || EMPTY_SESSIONS,
    sessionsLoading: sessionsQ.isFetching,
    lookupError: errorText(projectsQ.error, 'Could not load projects.') || errorText(sessionsQ.error, 'Could not load sessions.'),
  };
}
