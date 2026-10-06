import { useEffect, useRef, useState } from 'react';
import type { CIState, Check, PRChecks } from './upstreamApi';
import { CI_POLL_MS, PR_CHECKS_REFRESH_EVENT, cachePRChecks, getCachedPRChecks, isSettled } from './prChecksCache';

export interface ChecksState {
  state: CIState;
  checks: Check[];
  loading: boolean;
  loaded: boolean;
  error: boolean;
}

/** Both PR rows and conversation previews share the repository + SHA cache. */
export function usePRChecks(cacheKey: string, requestKey: string, visible: boolean, fetchChecks: (signal: AbortSignal, refresh: boolean) => Promise<PRChecks>): ChecksState {
  const [generation, setGeneration] = useState(0);
  const consumedRefresh = useRef(0);
  useEffect(() => {
    const refresh = () => setGeneration((g) => g + 1);
    window.addEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
    return () => window.removeEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
  }, []);
  const [result, setResult] = useState<{ key: string; data: PRChecks | null; loading: boolean; error: boolean }>({ key: requestKey, data: null, loading: false, error: false });
  useEffect(() => {
    if (!visible) return;
    const ctrl = new AbortController();
    let timer: number | undefined;
    let refresh = generation > consumedRefresh.current;
    const run = () => {
      const cached = refresh ? undefined : getCachedPRChecks(cacheKey);
      if (cached) {
        setResult({ key: requestKey, data: cached, loading: false, error: false });
        return;
      }
      setResult((prev) => ({ key: requestKey, data: prev.key === requestKey ? prev.data : null, loading: true, error: false }));
      const request = fetchChecks(ctrl.signal, refresh);
      if (refresh) consumedRefresh.current = generation;
      refresh = false;
      request.then((res) => {
        if (ctrl.signal.aborted) return;
        cachePRChecks(cacheKey, res);
        setResult({ key: requestKey, data: res, loading: false, error: false });
        if (!isSettled(res)) timer = window.setTimeout(run, CI_POLL_MS);
      }).catch(() => {
        if (ctrl.signal.aborted) return;
        setResult((prev) => ({ ...prev, key: requestKey, loading: false, error: true }));
        timer = window.setTimeout(run, CI_POLL_MS);
      });
    };
    run();
    return () => { ctrl.abort(); window.clearTimeout(timer); };
  }, [cacheKey, requestKey, visible, fetchChecks, generation]);
  const current = result.key === requestKey;
  return {
    state: current ? result.data?.state ?? 'unknown' : 'unknown',
    checks: current ? result.data?.checks ?? [] : [],
    loading: current && result.loading,
    loaded: current && result.data !== null,
    error: current && result.error,
  };
}

export const CI_LABEL: Record<CIState, string> = {
  unknown: 'No CI status', pending: 'Checks running',
  success: 'All checks passed', failure: 'Some checks failed',
};
