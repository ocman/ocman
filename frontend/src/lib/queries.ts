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
import { useQuery } from '@tanstack/react-query';
import { api } from './api';
import type {
  Session,
  Project,
  ActivityDay,
  MetricsDashboard,
  ModelUsage,
  HourlyData,
  HourlyTokensByModel,
} from './api';

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

// ---------------------------------------------------------------------------
// Projects list
// ---------------------------------------------------------------------------

export function useProjects(options?: { enabled?: boolean }) {
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
  return useQuery<MetricsDashboard>({
    queryKey: ['metrics', params],
    queryFn: ({ signal }) => api.metrics(params, signal),
    enabled: options?.enabled,
  });
}
