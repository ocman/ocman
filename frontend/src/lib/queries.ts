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
export * from './factoryQueries';
import { api } from './api';
import { useActivityScope } from './activityScopes';
import type {
  Session,
  Project,
  ActivityDay,
  MetricsPerformance,
  AnalyticsOverview,
  DatabaseSizeSample,
  SubscriptionUsageResponse,
  MetricsLog,
  MetricsLogKind,
  PermissionStats,
  ModelUsage,
  HourlyData,
  HourlyTokensByModel,
  InboxResponse,
  InboxItem,
} from './api';

export function useInbox(archived = false) {
  return useQuery<InboxResponse>({
    queryKey: archived ? ['inbox', 'archived'] : ['inbox'],
    queryFn: ({ signal }) => api.inbox(signal, archived),
    refetchInterval: 10_000,
  });
}

export function useMarkInboxItemRead() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, remoteId }: Pick<InboxItem, 'id' | 'remoteId'>) => api.markInboxItemRead(id, remoteId),
    onMutate: ({ id, remoteId }) => {
      client.setQueryData<InboxResponse>(['inbox'], (data) => data && {
        ...data,
        unreadTotal: Math.max(0, data.unreadTotal - (data.items.some((item) => item.id === id && item.remoteId === remoteId && !item.readAt) ? 1 : 0)),
        items: data.items.map((item) => item.id === id && item.remoteId === remoteId ? { ...item, readAt: Date.now() } : item),
      });
    },
    onSettled: () => client.invalidateQueries({ queryKey: ['inbox'] }),
  });
}

export function useMarkInboxItemUnread() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, remoteId }: Pick<InboxItem, 'id' | 'remoteId'>) => api.markInboxItemUnread(id, remoteId),
    onSuccess: (_result, { id, remoteId }) => {
      client.setQueryData<InboxResponse>(['inbox'], (data) => data && {
        ...data,
        unreadTotal: data.unreadTotal + (data.items.some((item) => item.id === id && item.remoteId === remoteId && item.readAt) ? 1 : 0),
        items: data.items.map((item) => item.id === id && item.remoteId === remoteId ? { ...item, readAt: undefined } : item),
      });
    },
    onSettled: () => client.invalidateQueries({ queryKey: ['inbox'] }),
  });
}

export function usePinInboxItem() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, remoteId, pinned }: Pick<InboxItem, 'id' | 'remoteId'> & { pinned: boolean }) => api.pinInboxItem(id, remoteId, pinned),
    onSuccess: (_result, { id, remoteId, pinned }) => client.setQueryData<InboxResponse>(['inbox'], (data) => data && {
      ...data,
      items: data.items
        .map((item) => item.id === id && item.remoteId === remoteId ? { ...item, pinned, pinnedAt: pinned ? Date.now() : undefined } : item)
        .sort((a, b) => Number(Boolean(b.pinned)) - Number(Boolean(a.pinned))
          || (b.pinnedAt ?? 0) - (a.pinnedAt ?? 0)
          || b.createdAt - a.createdAt
          || b.id.localeCompare(a.id)
          || b.remoteId.localeCompare(a.remoteId)),
    }),
    onSettled: () => client.invalidateQueries({ queryKey: ['inbox'] }),
  });
}

export function useRespondInboxPermission() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ permission, reply }: { permission: NonNullable<InboxItem['permission']>; reply: 'once' | 'always' | 'reject' }) =>
      api.respondPermission(permission.sessionId, permission.permissionId, reply, permission.platform),
    onSettled: () => client.invalidateQueries({ queryKey: ['inbox'] }),
  });
}

export function useArchiveInboxItems() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (items: Pick<InboxItem, 'id' | 'remoteId'>[]) => api.archiveInboxItems(items),
    onSuccess: (_result, items) => client.setQueryData<InboxResponse>(['inbox'], (data) => data && {
      ...data,
      items: data.items.filter((item) => !items.some((selected) => selected.id === item.id && selected.remoteId === item.remoteId)),
      unreadTotal: data.unreadTotal - items.filter((selected) => data.items.some((item) => item.id === selected.id && item.remoteId === selected.remoteId && !item.readAt)).length,
    }),
    onSettled: () => client.invalidateQueries({ queryKey: ['inbox'] }),
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
  dir?: string;
};

export function useMetrics(
  params?: MetricsParams,
  options?: { enabled?: boolean },
) {
  useActivityScope(options?.enabled === false ? undefined : 'metrics');
  return useQuery<MetricsPerformance>({
    queryKey: ['metrics', params],
    queryFn: ({ signal }) => api.metrics(params, signal),
    enabled: options?.enabled,
  });
}

export function useAnalyticsOverview() {
  useActivityScope('metrics');
  return useQuery<AnalyticsOverview>({
    queryKey: ['analyticsOverview'],
    queryFn: ({ signal }) => api.analyticsOverview(signal),
  });
}

export function useDatabaseSizes(params?: { days?: number }) {
  return useQuery<DatabaseSizeSample[]>({
    queryKey: ['databaseSizes', params],
    queryFn: ({ signal }) => api.databaseSizes(params, signal),
  });
}

export function useSubscriptionUsage() {
  // Cached for a minute: reopening the panel reuses it, and the server
  // caps upstream calls at one a minute regardless of Refresh clicks.
  return useQuery<SubscriptionUsageResponse>({
    queryKey: ['subscriptionUsage'],
    queryFn: ({ signal }) => api.subscriptionUsage(signal),
    refetchOnWindowFocus: false,
    staleTime: 60_000,
    gcTime: Infinity,
  });
}

export function useMetricLogs(
  params: MetricsParams & {
    kind: MetricsLogKind;
    limit?: number;
    offset?: number;
    sessionLimit?: number;
    sessionOffset?: number;
    projectLimit?: number;
    projectOffset?: number;
  },
) {
  useActivityScope('metrics');
  return useQuery<MetricsLog>({
    queryKey: ['metricLogs', params],
    queryFn: ({ signal }) => api.metricLogs(params, signal),
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
