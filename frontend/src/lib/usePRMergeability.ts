import { useEffect, useRef, useState } from 'react';
import { fetchPRMergeability, UpstreamApiError } from './upstreamApi';
import type { PR, PRMergeability } from './upstreamApi';
import { PR_CHECKS_REFRESH_EVENT } from './prChecksCache';
import { useDocumentVisible } from './usePanelVisible';

export function usePRMergeability(pr: PR, dir: string, remoteId: string, remote: string, visible: boolean) {
  const documentVisible = useDocumentVisible();
  const { number, status, mergeable, headSha, updatedAt } = pr;
  const [generation, setGeneration] = useState(0);
  const repository = `${pr.host}/${pr.repo}`;
  useEffect(() => {
    const refresh = (event: Event) => {
      const repositories = (event as CustomEvent<string[]>).detail;
      if (!repositories || repositories.includes(repository)) setGeneration((g) => g + 1);
    };
    window.addEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
    return () => window.removeEventListener(PR_CHECKS_REFRESH_EVENT, refresh);
  }, [repository]);
  const key = `${remoteId}\0${dir}\0${remote}\0${number}\0${headSha}\0${updatedAt}\0${generation}`;
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
    const load = async () => {
      let delay = 15_000;
      try {
        // Only the first read after a Refresh click bypasses the server cache.
        const refresh = generation > consumedRefresh.current;
        consumedRefresh.current = generation;
        const value = await fetchPRMergeability({ dir, remoteId, remote, number, signal: controller.signal, refresh });
        if (controller.signal.aborted) return;
        polling.failures = 0;
        polling.nextPollAt = 0;
        setResolved({ key, value });
        if (value.approved != null && (status !== 'open' || mergeable != null || value.mergeable != null)) {
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
      clearTimeout(timer);
    };
  }, [dir, key, mergeable, number, remote, remoteId, status, visible, documentVisible, generation]);
  const value = resolved?.key === key ? resolved.value : undefined;
  return { mergeable: mergeable ?? value?.mergeable, approved: value?.approved };
}
