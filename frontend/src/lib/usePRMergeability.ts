import { fetchPRMergeability, UpstreamApiError } from './upstreamApi';
import type { PR, PRMergeability } from './upstreamApi';
import { seedPRPoll, usePRQuery } from './prQuery';

type PRContext = Pick<PR, 'host' | 'repo' | 'number' | 'status' | 'headSha' | 'updatedAt' | 'mergeable'>;

const queryKey = (pr: PRContext, owner: string) => ['pr-status', `${pr.host}/${pr.repo}`, owner, pr.number, pr.headSha, pr.updatedAt, pr.status];

function statusPoll(pr: PRContext, value: PRMergeability) {
  const settled = value.approved != null && (pr.status !== 'open' || pr.mergeable != null || value.mergeable != null);
  return { value, nextPollAt: settled ? undefined : Date.now() + 15_000 };
}

export function seedPRMergeability(pr: PRContext, owner: string, value: PRMergeability) {
  seedPRPoll(queryKey(pr, owner), statusPoll(pr, value));
}

export function usePRMergeability(pr: PRContext, dir: string, remoteId: string, remote: string, visible: boolean, loadStatus?: (signal: AbortSignal, refresh: boolean) => Promise<PRMergeability>, refreshOnMount = false) {
  const { number, mergeable } = pr;
  const query = usePRQuery<PRMergeability>(queryKey(pr, remoteId), visible, async (signal, previous, refresh) => {
    refresh ||= previous?.refresh ?? false;
    try {
      const value = await (loadStatus ? loadStatus(signal, refresh) : fetchPRMergeability({ dir, remoteId, remote, number, signal, refresh }));
      return statusPoll(pr, value);
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
