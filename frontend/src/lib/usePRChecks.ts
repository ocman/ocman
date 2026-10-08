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
  const retryState = useRef({ cacheKey: '', requestKey: '', generation: -1, failures: 0, emptyResponses: 0, nextPollAt: 0 });
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
    if (retryState.current.cacheKey !== cacheKey || retryState.current.requestKey !== requestKey || retryState.current.generation !== generation) {
      retryState.current = { cacheKey, requestKey, generation, failures: 0, emptyResponses: 0, nextPollAt: 0 };
    }
    const polling = retryState.current;
    if (!visible) return;
    const ctrl = new AbortController();
    let timer: number | undefined;
    let inFlight = false;
    let settled = false;
    let refresh = polling.nextPollAt > 0 || generation > consumedRefresh.current || (refreshOnMount && refreshedKey.current !== requestKey);
    const schedule = (delay: number) => {
      polling.nextPollAt = Date.now() + delay;
      if (!document.hidden) timer = window.setTimeout(run, delay);
    };
    const retryDelay = () => Math.min(CI_POLL_MS * 2 ** Math.min(polling.failures++, 4), 60_000);
    const run = () => {
      timer = undefined;
      if (document.hidden || inFlight || settled) return;
      const cached = refresh ? undefined : getCachedPRChecks(cacheKey);
      if (cached) {
        settled = true;
        polling.nextPollAt = 0;
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
        if (!res.rateLimit?.limited) polling.failures = 0;
        polling.emptyResponses = !res.rateLimit?.limited && res.state === 'unknown' && res.checks.length === 0 ? polling.emptyResponses + 1 : 0;
        // Allow newly queued checks a minute to appear before caching "no CI".
        settled = isSettled(res, polling.emptyResponses >= 3);
        if (settled) polling.nextPollAt = 0;
        cachePRChecks(cacheKey, res, polling.emptyResponses >= 3);
        setResult({ key: requestKey, data: res, loading: false, error: false });
        if (!settled) {
          refresh = true;
          schedule(res.rateLimit?.limited ? Math.max(retryDelay(), Date.parse(res.rateLimit.resetAt ?? '') - Date.now() || 0) : polling.emptyResponses > 0 ? 30_000 : CI_POLL_MS);
        }
      }).catch((error: unknown) => {
        if (ctrl.signal.aborted) return;
        inFlight = false;
        polling.emptyResponses = 0;
        refresh = true;
        setResult((prev) => ({ ...prev, key: requestKey, loading: false, error: true }));
        const retryAt = error instanceof UpstreamApiError ? Date.parse(error.envelope?.error.retryAfter ?? '') : NaN;
        schedule(Math.max(retryDelay(), retryAt - Date.now() || 0));
      });
    };
    const visibility = () => {
      window.clearTimeout(timer);
      timer = undefined;
      if (!document.hidden && !inFlight && !settled) schedule(Math.max(0, polling.nextPollAt - Date.now()));
    };
    document.addEventListener('visibilitychange', visibility);
    if (polling.nextPollAt > Date.now()) schedule(polling.nextPollAt - Date.now());
    else run();
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
