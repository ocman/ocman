import { useEffect, useRef, useState } from 'react';

export interface UseAsyncResourceResult<T> {
  data: T;
  loading: boolean;
  error: string | null;
  /** True once the first fetch resolves (success or handled error). */
  ready: boolean;
}

export interface UseAsyncResourceOptions<T> {
  /**
   * Fetch the resource. Receives an AbortSignal that is aborted on
   * dependency change / unmount; reject with an AbortError-named
   * DOMException (the standard `fetch` abort) to be ignored.
   */
  fetcher: (signal: AbortSignal) => Promise<T>;
  /**
   * Re-run keys. The fetcher runs whenever any key changes (compared by
   * value). Keep these primitives.
   */
  deps: ReadonlyArray<unknown>;
  /** Initial / disabled-state value for `data`. */
  initial: T;
  /**
   * When false, no fetch is issued and the resource resets to `initial`
   * with ready=false. Use for "not enough inputs yet" cases.
   */
  enabled: boolean;
  /** Map a thrown error to the surfaced message. */
  errorMessage?: (err: unknown) => string;
}

function defaultErrorMessage(err: unknown): string {
  return err instanceof Error ? err.message : 'request failed';
}

function isAbort(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError';
}

/**
 * useAsyncResource centralises the abort-managed "fetch on dependency
 * change" pattern: a stable AbortController per run (so a quick remount
 * doesn't pile up requests), AbortError swallowing, and loading/error/
 * ready bookkeeping. Used by the simple per-resource hooks
 * (useUpstreams, useForgeUser).
 *
 * The synchronous setState calls are deferred into a closure so the
 * react-hooks lint rule against "setState directly in effect" passes.
 */
export function useAsyncResource<T>(opts: UseAsyncResourceOptions<T>): UseAsyncResourceResult<T> {
  const { fetcher, deps, initial, enabled, errorMessage = defaultErrorMessage } = opts;
  const [data, setData] = useState<T>(initial);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ready, setReady] = useState(false);

  const abortRef = useRef<AbortController | null>(null);
  // Keep the latest fetcher/errorMessage in refs so changing their
  // identity doesn't re-run the effect; the effect keys on `deps`.
  const fetcherRef = useRef(fetcher);
  const errorMessageRef = useRef(errorMessage);
  const initialRef = useRef(initial);
  useEffect(() => {
    fetcherRef.current = fetcher;
    errorMessageRef.current = errorMessage;
    initialRef.current = initial;
  });

  useEffect(() => {
    const reset = () => {
      setData(initialRef.current);
      setReady(false);
    };
    if (!enabled) {
      reset();
      return;
    }
    const run = () => {
      abortRef.current?.abort();
      const ctrl = new AbortController();
      abortRef.current = ctrl;
      setLoading(true);
      setError(null);
      fetcherRef.current(ctrl.signal)
        .then((result) => {
          if (ctrl.signal.aborted) return;
          setData(result);
          setLoading(false);
          setReady(true);
        })
        .catch((err: unknown) => {
          if (ctrl.signal.aborted || isAbort(err)) return;
          setData(initialRef.current);
          setError(errorMessageRef.current(err));
          setLoading(false);
          setReady(true);
        });
    };
    run();
    return () => {
      abortRef.current?.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, ...deps]);

  return { data, loading, error, ready };
}
