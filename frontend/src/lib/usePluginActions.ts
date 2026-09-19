import { useQueries, useQuery } from '@tanstack/react-query';
import { useLocation } from 'react-router-dom';
import { api } from './api';
import { useApiStore } from './apiStore';
import { pluginActions, type PluginActionContext, type PluginActionRequest } from './plugins';

export function usePluginActions(projectDirectory: string | undefined, enabled: boolean) {
  const location = useLocation();
  const sessions = useApiStore((s) => s.cachedSessions);
  const route = location.pathname.split('/')[1] || 'sessions';
  const ownerId = new URLSearchParams(location.search).get('remoteId') || 'local';
  const sessionId = route === 'session' ? location.pathname.split('/')[2] : undefined;
  const cached = sessionId
    ? sessions?.find((s) => s.id === sessionId)
    : route === 'project'
      ? sessions?.find((s) => s.directory === projectDirectory && (s.remoteId || 'local') === ownerId)
      : undefined;
  // The search cache only holds recent sessions. Resolve older active contexts separately.
  const current = useQuery({
    queryKey: ['plugin-action-context', sessionId, projectDirectory, ownerId],
    queryFn: async ({ signal }) => sessionId
      ? (await api.session(sessionId, 1, 0, signal)).session
      : (await api.sessions({ dir: projectDirectory, limit: 0 }, signal))
        .find((s) => s.directory === projectDirectory && (s.remoteId || 'local') === ownerId) ?? null,
    initialData: cached,
    enabled: enabled && !cached && (!!sessionId || (route === 'project' && !!projectDirectory)),
    retry: false,
    gcTime: 0,
  });
  const session = cached ?? current.data;
  const context: PluginActionContext = {
    ownerId: session?.remoteId || ownerId,
    projectId: session?.projectId,
    sessionId: sessionId ? session?.id : undefined,
    route: ['sessions', 'projects', 'project', 'session', 'settings', 'inbox', 'routines', 'factory', 'analytics'].includes(route) ? route : undefined,
  };
  const placements: PluginActionRequest['placement'][] = ['global'];
  if (context.projectId) placements.push('project');
  if (context.sessionId) placements.push('session');
  const queries = useQueries({ queries: placements.map((placement) => ({
    queryKey: ['plugin-actions', placement, context],
    queryFn: ({ signal }: { signal: AbortSignal }) => pluginActions.list(placement, context, signal),
    enabled,
    retry: false,
    // Recheck enablement and grants each time the palette opens.
    staleTime: 0,
    gcTime: 0,
  })) });
  return {
    context,
    actions: enabled ? queries.flatMap((query) => query.isError ? [] : query.data ?? []) : [],
    unavailable: enabled && (current.isError || queries.some((query) => query.isError)),
  };
}
