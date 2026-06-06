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
 * directory. Per OQ-C (architecture doc), the result is memoised per
 * directory so re-mounting RightPanel doesn't trigger a re-detection.
 *
 * - directory undefined → no fetch; returns empty + ready=false.
 * - 404 (not a git repo) → empty list + ready=true (no error surfaced).
 * - other network errors → error string set, list empty.
 */
export function useUpstreams(directory: string | undefined): UseUpstreamsResult {
  // Memoise on the trimmed string so callers can pass props directly.
  const dir = useMemo(() => (directory ?? '').trim(), [directory]);

  const { data, loading, error, ready } = useAsyncResource<Upstream[]>({
    fetcher: (signal) => fetchUpstreams(dir, signal),
    deps: [dir],
    initial: EMPTY,
    enabled: !!dir,
    errorMessage: (err) => (err instanceof Error ? err.message : 'failed to detect upstreams'),
  });

  return { upstreams: data, loading, error, ready };
}
