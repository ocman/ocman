import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchPRMergeability, UpstreamApiError } from './upstreamApi';
import type { PR, PRMergeability } from './upstreamApi';
import { PR_CHECKS_REFRESH_EVENT } from './prChecksCache';
import { useDocumentVisible } from './usePanelVisible';
import { acquirePRStatus } from './prStatusCache';

export function usePRMergeability(pr: Pick<PR, 'host' | 'repo' | 'number' | 'status' | 'headSha' | 'updatedAt' | 'mergeable'>, dir: string, remoteId: string, remote: string, visible: boolean, loadStatus?: (signal: AbortSignal, refresh: boolean) => Promise<PRMergeability>, refreshOnMount = false) {
  const documentVisible = useDocumentVisible();
  const { number, status, mergeable, headSha, updatedAt } = pr;
  const [generation, setGeneration] = useState(refreshOnMount ? 1 : 0);
  const repository = `${pr.host}/${pr.repo}`;
  useEffect(() => {
    const refresh = (event: Event) => {
      const repositories = (event as CustomEvent<string[]>).detail;
      if (!repositories || repositories.includes(repository)) setGeneration((g) => g + 1);
    };
    window.addEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
    return () => window.removeEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
  }, [repository]);
  const cacheKey = `${remoteId}\0${repository}\0${number}\0${headSha}\0${updatedAt}\0${status}`;
  const key = `${cacheKey}\0${dir}\0${remote}\0${generation}`;
  const knownMergeability = status !== 'open' || mergeable != null;
  const fetchStatus = useCallback(async (signal: AbortSignal, refresh: boolean) => {
    const value = await (loadStatus ? loadStatus(signal, refresh) : fetchPRMergeability({ dir, remoteId, remote, number, signal, refresh }));
    return { ...value, mergeable: mergeable ?? value.mergeable };
  }, [loadStatus, dir, remoteId, remote, number, mergeable]);
  const [resolved, setResolved] = useState<{ key: string; value: PRMergeability }>();
  const retry = useRef({ key: '', failures: 0, nextPollAt: 0, stopped: false });
  // A Refresh click is consumed once, independent of later automatic PR
  // identity changes (new head, edited title) that also change the key.
  const consumedRefresh = useRef(0);
  useEffect(() => {
    if (retry.current.key !== key) retry.current = { key, failures: 0, nextPollAt: 0, stopped: false };
    const polling = retry.current;
    if (!visible || !documentVisible || polling.stopped) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    let release: (() => void) | undefined;
    const load = async () => {
      let delay = 15_000;
      try {
        // Only the first read after a Refresh click bypasses the server cache.
        const refresh = generation > consumedRefresh.current;
        release?.();
        const request = acquirePRStatus(cacheKey, (signal) => fetchStatus(signal, refresh), refresh, knownMergeability);
        release = request.release;
        const value = await request.promise;
        if (controller.signal.aborted) return;
        consumedRefresh.current = generation;
        polling.failures = 0;
        polling.nextPollAt = 0;
        setResolved({ key, value });
        if (value.approved != null && (knownMergeability || value.mergeable != null)) {
          polling.stopped = true;
          return;
        }
      } catch (error) {
        if (controller.signal.aborted) return;
        const status = error instanceof UpstreamApiError ? error.envelope?.error.upstreamStatus || error.status : 0;
        if (status >= 400 && status < 500 && status !== 408 && status !== 429) {
          polling.stopped = true;
          return;
        }
        const retryAt = error instanceof UpstreamApiError ? Date.parse(error.envelope?.error.retryAfter ?? '') : NaN;
        delay = Math.max(Math.min(15_000 * 2 ** Math.min(polling.failures++, 2), 60_000), retryAt - Date.now() || 0);
      }
      if (!controller.signal.aborted) {
        polling.nextPollAt = Date.now() + delay;
        timer = setTimeout(load, delay);
      }
    };
    if (polling.nextPollAt > Date.now()) timer = setTimeout(load, polling.nextPollAt - Date.now());
    else void load();
    return () => {
      controller.abort();
      release?.();
      clearTimeout(timer);
    };
  }, [key, cacheKey, knownMergeability, visible, documentVisible, generation, fetchStatus]);
  const value = resolved?.key === key ? resolved.value : undefined;
  return { mergeable: mergeable ?? value?.mergeable, approved: value?.approved };
}
