import { useEffect, useRef } from 'react';
import { hashKey, useQuery, useQueryClient, type QueryKey } from '@tanstack/react-query';
import { coalescedRefresh } from './coalescedRefresh';
import { useDocumentVisible } from './usePanelVisible';

// Window that folds a burst of dirty ticks (edit/write part updates) into
// one refetch.
export const DIRTY_REFRESH_MS = 500;

export interface SessionResourceOptions {
  /**
   * When false, the hook returns `emptyValue` with `loading: false` and
   * never fires a request (the platform doesn't support the resource).
   */
  enabled?: boolean;
  /** Each change schedules a coalesced refetch; wired to the SSE dirty counter. */
  dirtyTick?: number;
}

export interface SessionResourceResult<T> {
  data: T | null;
  loading: boolean;
  error: string | null;
  /** Refetch immediately. No-op while disabled or hidden. */
  refresh: () => void;
}

/**
 * A per-session resource that refetches when `dirtyTick` changes. Fetching
 * pauses while the document is hidden; cached data is kept and refreshed
 * on return. `key` is undefined when there is nothing to fetch.
 */
export function useSessionResourceQuery<T>(
  key: QueryKey | undefined,
  fetch: (signal: AbortSignal) => Promise<T>,
  emptyValue: T,
  fallbackError: string,
  { enabled = true, dirtyTick = 0 }: SessionResourceOptions = {},
): SessionResourceResult<T> {
  const visible = useDocumentVisible();
  const client = useQueryClient();
  const queryKey = key ?? ['session-resource', 'none'];
  const active = enabled && !!key;
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => fetch(signal),
    enabled: active && visible,
    staleTime: 0,
    refetchOnWindowFocus: false,
  });

  const keyHash = hashKey(queryKey);
  const refresher = useRef<ReturnType<typeof coalescedRefresh> | null>(null);
  useEffect(() => {
    // Wait for a read already in flight, then refetch, so the result is
    // never older than the last tick. Inactive (hidden) queries are only
    // marked stale and refetch when they become visible again.
    const next = coalescedRefresh(async () => {
      await client.getQueryCache().get(keyHash)?.promise?.catch(() => {});
      await client.invalidateQueries({ predicate: (q) => q.queryHash === keyHash }, { cancelRefetch: false });
    }, DIRTY_REFRESH_MS);
    refresher.current = next;
    return next.reset;
  }, [client, keyHash]);
  const lastTick = useRef(dirtyTick);
  useEffect(() => {
    if (lastTick.current === dirtyTick) return;
    lastTick.current = dirtyTick;
    if (active) refresher.current?.schedule();
  }, [dirtyTick, active]);

  if (!enabled) return { data: emptyValue, loading: false, error: null, refresh: () => {} };
  const { error } = query;
  return {
    data: query.data ?? null,
    loading: active && (query.isPending || query.isFetching),
    error: error ? (error instanceof Error ? error.message : fallbackError) : null,
    refresh: () => {
      if (active && visible) void query.refetch();
    },
  };
}
