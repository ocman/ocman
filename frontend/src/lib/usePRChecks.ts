import type { CIState, Check, PRChecks } from './upstreamApi';
import { UpstreamApiError } from './upstreamApi';
import { CI_POLL_MS, cachePRChecks, getCachedPRChecks, isSettled } from './prChecksCache';
import { usePRQuery } from './prQuery';

export interface ChecksState {
  state: CIState;
  checks: Check[];
  loading: boolean;
  loaded: boolean;
  error: boolean;
}

/** Both PR rows and conversation previews share the repository + SHA cache. */
export function usePRChecks(cacheKey: string, requestKey: string, visible: boolean, fetchChecks: (signal: AbortSignal, refresh: boolean) => Promise<PRChecks>, refreshOnMount = false): ChecksState {
  const query = usePRQuery<PRChecks>(['pr-checks', cacheKey.split('@')[0], cacheKey, requestKey], visible, async (signal, previous, refresh) => {
    const cached = !refresh && !previous ? getCachedPRChecks(cacheKey) : undefined;
    if (cached) return { value: cached };
    let failures = previous?.failures ?? 0;
    const backoff = (resetAt?: string) => Math.max(Math.min(CI_POLL_MS * 2 ** Math.min(failures++, 4), 60_000), Date.parse(resetAt ?? '') - Date.now() || 0);
    try {
      const value = await fetchChecks(signal, refresh);
      signal.throwIfAborted();
      const limited = value.rateLimit?.limited || value.rateLimit?.stale;
      if (!limited) failures = 0;
      const emptyResponses = !limited && value.state === 'unknown' && !value.checks.length ? (previous?.emptyResponses ?? 0) + 1 : 0;
      const settled = isSettled(value, emptyResponses >= 3);
      cachePRChecks(cacheKey, value, emptyResponses >= 3);
      const delay = limited ? backoff(value.rateLimit?.resetAt) : emptyResponses ? 30_000 : CI_POLL_MS;
      return { value, failures, emptyResponses, nextPollAt: settled ? undefined : Date.now() + delay };
    } catch (error) {
      signal.throwIfAborted();
      const delay = backoff(error instanceof UpstreamApiError ? error.envelope?.error.retryAfter : undefined);
      return { value: previous?.value, error, failures, emptyResponses: 0, nextPollAt: Date.now() + delay };
    }
  }, refreshOnMount);
  return {
    state: query.data?.value?.state ?? 'unknown',
    checks: query.data?.value?.checks ?? [],
    loading: query.isFetching,
    loaded: !!query.data?.value,
    error: !!query.data?.error,
  };
}

export const CI_LABEL: Record<CIState, string> = {
  unknown: 'No CI status', pending: 'Checks running',
  success: 'All checks passed', failure: 'Some checks failed',
};
