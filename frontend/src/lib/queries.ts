/**
 * TanStack Query hooks for shared data fetching.
 *
 * These hooks replace the hand-rolled polling + AbortController patterns
 * in Dashboard, ProjectDetail, and SessionDetail with TanStack Query's
 * built-in dedup, cancellation, stale-while-revalidate, and visibility
 * pausing.
 *
 * The `apiStore` Zustand store remains for mutations, command palette
 * state, sidebar layout, and other non-GET concerns.
 *
 * See spec/ui-responsiveness Wave 3 (P4, P5).
 */
import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { api } from './api';
import { useActivityScope } from './activityScopes';
import type {
  Session,
  Project,
  ActivityDay,
  MetricsDashboard,
  PermissionStats,
  ModelUsage,
  HourlyData,
  HourlyTokensByModel,
  FactoryStatus,
  WorkEpic,
  CreateWorkEpicRequest,
	FactoryPlan,
	FactoryPlanGraph,
	FactoryPlanDecisionRequest,
} from './api';

export function useFactoryStatus() {
  return useQuery<FactoryStatus>({
    queryKey: ['factory-status'],
    queryFn: ({ signal }) => api.factoryStatus(signal),
    refetchInterval: 10_000,
  });
}

export function useWorkEpics(enabled = true) {
  return useQuery<WorkEpic[]>({
    queryKey: ['factory-epics'],
    queryFn: ({ signal }) => api.factoryEpics(signal),
    enabled,
    refetchInterval: 10_000,
  });
}

export function useWorkEpic(id: string) {
  return useQuery<WorkEpic>({
    queryKey: ['factory-epics', id],
    queryFn: ({ signal }) => api.factoryEpic(id, signal),
    enabled: Boolean(id),
  });
}

export function useCreateWorkEpic() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (request: CreateWorkEpicRequest) => api.createFactoryEpic(request),
    onSuccess: async (epic) => {
      queryClient.setQueryData<WorkEpic[]>(['factory-epics'], (epics = []) => [
        epic,
        ...epics.filter((item) => item.id !== epic.id),
      ]);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['factory-status'] }),
        queryClient.invalidateQueries({ queryKey: ['factory-epics'] }),
      ]);
    },
  });
}

function setEpicPlan(queryClient: QueryClient, id: string, plan: FactoryPlan) {
	queryClient.setQueryData<WorkEpic[]>(['factory-epics'], (epics = []) => epics.map((epic) => epic.id === id ? { ...epic, plan } : epic));
	queryClient.setQueryData<WorkEpic>(['factory-epics', id], (epic) => epic ? { ...epic, plan } : epic);
}

export function useMutateFactoryPlan(id: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: ({ expectedRevision, graph }: { expectedRevision: number; graph: FactoryPlanGraph }) => api.mutateFactoryPlan(id, expectedRevision, graph),
		onSuccess: (result) => setEpicPlan(queryClient, id, result.plan),
	});
}

export function useAddFactoryPlanningWork(id: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: ({ expectedRevision, target }: { expectedRevision: number; target: FactoryPlanGraph['targets'][number] }) => api.addFactoryPlanningWork(id, expectedRevision, target),
		onSuccess: (result) => setEpicPlan(queryClient, id, result.plan),
	});
}

export function useDecideFactoryPlan(id: string) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: ({ action, request }: { action: 'approve' | 'revise' | 'reject' | 'cancel'; request: FactoryPlanDecisionRequest }) => api.decideFactoryPlan(id, action, request),
		onSuccess: (plan) => setEpicPlan(queryClient, id, plan),
	});
}

// ---------------------------------------------------------------------------
// Sessions list
// ---------------------------------------------------------------------------

type SessionsParams = {
  dir?: string;
  /**
   * Lookback window in hours. Preferred over a raw `since` timestamp
   * because it produces a stable query key (e.g. `12` for "last 12h")
   * instead of a moving `Date.now()` value that would bust the cache
   * on every render.
   */
  sinceHours?: number;
  limit?: number;
};

/**
 * Shared sessions-list query. Multiple components (Dashboard, ProjectDetail,
 * SessionDetail sidebar) can call this with the same params and TanStack
 * deduplicates to a single in-flight request.
 *
 * `sinceHours` is preferred over a raw `since` timestamp because
 * `Date.now()` is impure and would change the query key on every render,
 * defeating TanStack's caching. The actual timestamp is computed inside
 * the `queryFn` at fetch time.
 *
 * @param params  Filter params forwarded to `/api/sessions`.
 * @param options.refetchInterval  Per-consumer polling interval (ms).
 * @param options.enabled          Whether the query should run.
 */
export function useSessions(
  params?: SessionsParams,
  options?: { refetchInterval?: number; enabled?: boolean },
) {
  useActivityScope(options?.enabled === false ? undefined : 'sessions');
  // Build a stable query key from the params. `sinceHours` is a stable
  // number (e.g. 12, 168) rather than a moving timestamp.
  const key = params?.dir
    ? ['sessions', { dir: params.dir, sinceHours: params.sinceHours, limit: params.limit }]
    : ['sessions', { sinceHours: params?.sinceHours, limit: params?.limit }];

  return useQuery<Session[]>({
    queryKey: key,
    queryFn: ({ signal }) => {
      // Compute the actual `since` timestamp at fetch time so the query
      // key stays stable across renders.
      const since = params?.sinceHours
        ? Date.now() - params.sinceHours * 60 * 60 * 1000
        : undefined;
      return api.sessions({ dir: params?.dir, since, limit: params?.limit }, signal)
        .then((r) => r ?? []);
    },
    refetchInterval: options?.refetchInterval,
    enabled: options?.enabled,
  });
}

/**
 * Insert (or replace) a provisional session row into every cached
 * `['sessions', ...]` list so a freshly-created session shows up
 * instantly, before the authoritative refetch that
 * `invalidateQueries(['sessions'])` triggers overwrites it. No-op for
 * lists that don't have data yet (they'll fetch fresh anyway) or that
 * are directory-scoped to a different directory.
 */
export function insertProvisionalSession(qc: QueryClient, session: Session): void {
  for (const [key, existing] of qc.getQueriesData<Session[]>({ queryKey: ['sessions'] })) {
    if (!existing) continue; // no data yet → it'll fetch fresh anyway
    // Respect a directory filter: don't inject a /repo/a session into a
    // list scoped to /repo/b. The dir (when present) lives in the
    // second key segment (see useSessions' queryKey).
    const scopedDir = (key[1] as { dir?: string } | undefined)?.dir;
    if (scopedDir && scopedDir !== session.directory) continue;
    if (existing.some((s) => s.id === session.id)) continue;
    qc.setQueryData<Session[]>(key, [session, ...existing]);
  }
}

// ---------------------------------------------------------------------------
// Projects list
// ---------------------------------------------------------------------------

export function useProjects(options?: { enabled?: boolean }) {
  useActivityScope(options?.enabled === false ? undefined : 'projects');
  return useQuery<Project[]>({
    queryKey: ['projects'],
    queryFn: ({ signal }) => api.projects(signal),
    staleTime: 30_000, // projects change rarely
    enabled: options?.enabled,
  });
}

// ---------------------------------------------------------------------------
// Usage tab queries
// ---------------------------------------------------------------------------

export function useActivity(
  params?: { days?: number; model?: string; dir?: string },
  options?: { enabled?: boolean },
) {
  useActivityScope(options?.enabled === false ? undefined : 'metrics');
  return useQuery<ActivityDay[]>({
    queryKey: ['activity', params],
    queryFn: ({ signal }) => api.activity(params, signal),
    enabled: options?.enabled,
  });
}

export function useModels(
  params?: { days?: number; dir?: string },
  options?: { enabled?: boolean },
) {
  useActivityScope(options?.enabled === false ? undefined : 'metrics');
  return useQuery<ModelUsage[]>({
    queryKey: ['models', params],
    queryFn: ({ signal }) => api.models(params, signal),
    enabled: options?.enabled,
  });
}

export function useHourly(
  params?: { days?: number; dir?: string },
  options?: { enabled?: boolean },
) {
  useActivityScope(options?.enabled === false ? undefined : 'metrics');
  return useQuery<HourlyData[]>({
    queryKey: ['hourly', params],
    queryFn: ({ signal }) => api.hourly(params, signal),
    enabled: options?.enabled,
  });
}

export function useHourlyTokens(
  params?: { days?: number; model?: string; dir?: string },
  options?: { enabled?: boolean },
) {
  useActivityScope(options?.enabled === false ? undefined : 'metrics');
  return useQuery<HourlyTokensByModel[]>({
    queryKey: ['hourlyTokens', params],
    queryFn: ({ signal }) => api.hourlyTokens(params, signal),
    enabled: options?.enabled,
  });
}

// ---------------------------------------------------------------------------
// Metrics (Stats tab)
// ---------------------------------------------------------------------------

type MetricsParams = {
  agent?: string;
  model?: string;
  days?: number;
  limit?: number;
  offset?: number;
  sessionLimit?: number;
  sessionOffset?: number;
  projectLimit?: number;
  projectOffset?: number;
  dir?: string;
};

export function useMetrics(
  params?: MetricsParams,
  options?: { enabled?: boolean },
) {
  useActivityScope(options?.enabled === false ? undefined : 'metrics');
  return useQuery<MetricsDashboard>({
    queryKey: ['metrics', params],
    queryFn: ({ signal }) => api.metrics(params, signal),
    enabled: options?.enabled,
  });
}

export function usePermissionStats(
  params?: { days?: number; dir?: string },
  options?: { enabled?: boolean },
) {
  useActivityScope(options?.enabled === false ? undefined : 'metrics');
  return useQuery<PermissionStats>({
    queryKey: ['permissionStats', params],
    queryFn: ({ signal }) => api.permissionStats(params, signal),
    enabled: options?.enabled,
  });
}
