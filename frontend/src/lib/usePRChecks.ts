import { useEffect, useRef, useState } from 'react';
import type { CIState, Check, PRChecks } from './upstreamApi';
import { UpstreamApiError } from './upstreamApi';
import { CI_POLL_MS, PR_CHECKS_REFRESH_EVENT, cachePRChecks, getCachedPRChecks, isSettled } from './prChecksCache';

export interface ChecksState {
  state: CIState;
  checks: Check[];
  loading: boolean;
  loaded: boolean;
  error: boolean;
}

/** Both PR rows and conversation previews share the repository + SHA cache. */
export function usePRChecks(cacheKey: string, requestKey: string, visible: boolean, fetchChecks: (signal: AbortSignal, refresh: boolean) => Promise<PRChecks>, refreshOnMount = false): ChecksState {
  const [generation, setGeneration] = useState(refreshOnMount ? 1 : 0);
  const consumedRefresh = useRef(0);
  const refreshedKey = useRef<string | undefined>(undefined);
  useEffect(() => {
    const refresh = (event: Event) => {
      const repositories = (event as CustomEvent<string[]>).detail;
      if (!repositories || repositories.some((repo) => cacheKey.startsWith(`${repo}@`))) {
        refreshedKey.current = undefined;
        setGeneration((g) => g + 1);
      }
    };
    window.addEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
    return () => window.removeEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
  }, [cacheKey]);
  const [result, setResult] = useState<{ key: string; data: PRChecks | null; loading: boolean; error: boolean }>({ key: requestKey, data: null, loading: false, error: false });
  useEffect(() => {
    if (!visible) return;
    const ctrl = new AbortController();
    let timer: number | undefined;
    let failures = 0;
    let emptyResponses = 0;
    let nextPollAt = 0;
    let inFlight = false;
    let settled = false;
    let refresh = generation > consumedRefresh.current || (refreshOnMount && refreshedKey.current !== requestKey);
    const schedule = (delay: number) => {
      nextPollAt = Date.now() + delay;
      if (!document.hidden) timer = window.setTimeout(run, delay);
    };
    const retryDelay = () => Math.min(CI_POLL_MS * 2 ** Math.min(failures++, 4), 60_000);
    const run = () => {
      timer = undefined;
      if (document.hidden || inFlight || settled) return;
      const cached = refresh ? undefined : getCachedPRChecks(cacheKey);
      if (cached) {
        settled = true;
        setResult({ key: requestKey, data: cached, loading: false, error: false });
        return;
      }
      setResult((prev) => ({ key: requestKey, data: prev.key === requestKey ? prev.data : null, loading: true, error: false }));
      inFlight = true;
      const request = fetchChecks(ctrl.signal, refresh);
      const wasRefresh = refresh;
      refresh = false;
      request.then((res) => {
        if (ctrl.signal.aborted) return;
        inFlight = false;
        if (wasRefresh) {
          consumedRefresh.current = generation;
          refreshedKey.current = requestKey;
        }
        if (!res.rateLimit?.limited) failures = 0;
        emptyResponses = !res.rateLimit?.limited && res.state === 'unknown' && res.checks.length === 0 ? emptyResponses + 1 : 0;
        // Allow newly queued checks a minute to appear before caching "no CI".
        settled = isSettled(res, emptyResponses >= 3);
        cachePRChecks(cacheKey, res, emptyResponses >= 3);
        setResult({ key: requestKey, data: res, loading: false, error: false });
        if (!settled) {
          refresh = true;
          schedule(res.rateLimit?.limited ? Math.max(retryDelay(), Date.parse(res.rateLimit.resetAt ?? '') - Date.now() || 0) : emptyResponses > 0 ? 30_000 : CI_POLL_MS);
        }
      }).catch((error: unknown) => {
        if (ctrl.signal.aborted) return;
        inFlight = false;
        emptyResponses = 0;
        refresh = true;
        setResult((prev) => ({ ...prev, key: requestKey, loading: false, error: true }));
        const retryAt = error instanceof UpstreamApiError ? Date.parse(error.envelope?.error.retryAfter ?? '') : NaN;
        schedule(Math.max(retryDelay(), retryAt - Date.now() || 0));
      });
    };
    const visibility = () => {
      window.clearTimeout(timer);
      timer = undefined;
      if (!document.hidden && !inFlight && !settled) schedule(Math.max(0, nextPollAt - Date.now()));
    };
    document.addEventListener('visibilitychange', visibility);
    run();
    return () => { ctrl.abort(); window.clearTimeout(timer); document.removeEventListener('visibilitychange', visibility); };
  }, [cacheKey, requestKey, visible, fetchChecks, generation, refreshOnMount]);
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
