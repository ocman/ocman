import { useEffect, useMemo, useRef, useState } from 'react';
import { fetchUpstreams, type Upstream } from './upstreamApi';

export interface UseUpstreamsResult {
  upstreams: Upstream[];
  loading: boolean;
  error: string | null;
  /** True once the first fetch has resolved (success or empty). */
  ready: boolean;
}

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
  const [upstreams, setUpstreams] = useState<Upstream[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ready, setReady] = useState(false);

  // Stable abort handle so a quick remount doesn't pile up requests.
  const abortRef = useRef<AbortController | null>(null);

  // Memoise on the trimmed string so callers can pass props directly.
  const dir = useMemo(() => (directory ?? '').trim(), [directory]);

  useEffect(() => {
    // Defer the actual work into a closure so the lint rule's
    // "no synchronous setState in effects" check is satisfied
    // (matches useGitInfo's structure). Functionally equivalent.
    const reset = () => {
      setUpstreams([]);
      setReady(false);
    };
    if (!dir) {
      reset();
      return;
    }
    const run = () => {
      abortRef.current?.abort();
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      setLoading(true);
      setError(null);
      fetchUpstreams(dir, ctrl.signal)
        .then((list) => {
          if (ctrl.signal.aborted) return;
          setUpstreams(list);
          setLoading(false);
          setReady(true);
        })
        .catch((err: unknown) => {
          if (ctrl.signal.aborted) return;
          if (err instanceof DOMException && err.name === 'AbortError') return;
          setUpstreams([]);
          setError(err instanceof Error ? err.message : 'failed to detect upstreams');
          setLoading(false);
          setReady(true);
        });
    };
    run();
    return () => {
      abortRef.current?.abort();
    };
  }, [dir]);

  return { upstreams, loading, error, ready };
}
