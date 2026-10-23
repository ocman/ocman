import { fetchPRMergeability, UpstreamApiError } from './upstreamApi';
import type { PR, PRMergeability } from './upstreamApi';
import { usePRQuery } from './prQuery';

export function usePRMergeability(pr: Pick<PR, 'host' | 'repo' | 'number' | 'status' | 'headSha' | 'updatedAt' | 'mergeable'>, dir: string, remoteId: string, remote: string, visible: boolean, loadStatus?: (signal: AbortSignal, refresh: boolean) => Promise<PRMergeability>, refreshOnMount = false) {
  const { number, status, mergeable, headSha, updatedAt } = pr;
  const knownMergeability = status !== 'open' || mergeable != null;
  const query = usePRQuery<PRMergeability>(['pr-status', `${pr.host}/${pr.repo}`, remoteId, number, headSha, updatedAt, status], visible, async (signal, previous, refresh) => {
    refresh ||= previous?.refresh ?? false;
    try {
      const value = await (loadStatus ? loadStatus(signal, refresh) : fetchPRMergeability({ dir, remoteId, remote, number, signal, refresh }));
      const settled = value.approved != null && (knownMergeability || value.mergeable != null);
      return { value, nextPollAt: settled ? undefined : Date.now() + 15_000 };
    } catch (error) {
      signal.throwIfAborted();
      const status = error instanceof UpstreamApiError ? error.envelope?.error.upstreamStatus || error.status : 0;
      const permanent = status >= 400 && status < 500 && status !== 408 && status !== 429;
      const failures = previous?.failures ?? 0;
      const retryAt = error instanceof UpstreamApiError ? Date.parse(error.envelope?.error.retryAfter ?? '') : NaN;
      const delay = Math.max(Math.min(15_000 * 2 ** Math.min(failures, 2), 60_000), retryAt - Date.now() || 0);
      return { value: previous?.value, error, refresh, failures: failures + 1, nextPollAt: permanent ? undefined : Date.now() + delay };
    }
  }, refreshOnMount);
  return { mergeable: mergeable ?? query.data?.value?.mergeable, approved: query.data?.value?.approved };
}
