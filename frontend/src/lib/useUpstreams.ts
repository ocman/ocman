import { useQuery } from '@tanstack/react-query';
import { fetchUpstreams, type Upstream } from './upstreamApi';

export interface UseUpstreamsResult {
  upstreams: Upstream[];
  loading: boolean;
  error: string | null;
  /** True once the first fetch has resolved (success or empty). */
  ready: boolean;
  refresh: () => void;
}

const EMPTY: Upstream[] = [];

/**
 * useUpstreams subscribes to /api/project/upstreams for the given
 * directory and owning machine.
 *
 * - directory undefined → no fetch; returns empty + ready=false.
 * - 404 (not a git repo) → empty list + ready=true (no error surfaced).
 * - other network errors → error string set; a failed refetch keeps the
 *   last list.
 */
export function useUpstreams(directory: string | undefined, remoteId: string): UseUpstreamsResult {
  const dir = (directory ?? '').trim();
  const query = useQuery({
    queryKey: ['upstreams', remoteId, dir],
    queryFn: ({ signal }) => fetchUpstreams(dir, remoteId, signal),
    enabled: !!dir,
  });

  if (!dir) return { upstreams: EMPTY, loading: false, error: null, ready: false, refresh: () => {} };
  const { error } = query;
  return {
    upstreams: query.data ?? EMPTY,
    loading: query.isPending || query.isFetching,
    error: error ? (error instanceof Error ? error.message : 'failed to detect upstreams') : null,
    ready: !query.isPending,
    refresh: () => void query.refetch(),
  };
}
