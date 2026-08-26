import { useMemo } from 'react';
import { fetchUpstreams, type Upstream } from './upstreamApi';
import { useAsyncResource } from './useAsyncResource';

export interface UseUpstreamsResult {
  upstreams: Upstream[];
  loading: boolean;
  error: string | null;
  /** True once the first fetch has resolved (success or empty). */
  ready: boolean;
}

const EMPTY: Upstream[] = [];

/**
 * useUpstreams subscribes to /api/project/upstreams for the given
 * directory and owning machine.
 *
 * - directory undefined → no fetch; returns empty + ready=false.
 * - 404 (not a git repo) → empty list + ready=true (no error surfaced).
 * - other network errors → error string set, list empty.
 */
export function useUpstreams(directory: string | undefined, remoteId = 'local'): UseUpstreamsResult {
  // Memoise on the trimmed string so callers can pass props directly.
  const dir = useMemo(() => (directory ?? '').trim(), [directory]);

  const { data, loading, error, ready } = useAsyncResource<Upstream[]>({
    fetcher: (signal) => fetchUpstreams(dir, remoteId, signal),
    deps: [dir, remoteId],
    initial: EMPTY,
    enabled: !!dir,
    errorMessage: (err) => (err instanceof Error ? err.message : 'failed to detect upstreams'),
  });

  return { upstreams: data, loading, error, ready };
}
