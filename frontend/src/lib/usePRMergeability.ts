import { useEffect, useState } from 'react';
import { fetchPRMergeability } from './upstreamApi';
import type { PR } from './upstreamApi';
import { PR_CHECKS_REFRESH_EVENT } from './prChecksCache';

export function usePRMergeability(pr: PR, dir: string, remoteId: string, remote: string, visible: boolean) {
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
  const [resolved, setResolved] = useState<{ key: string; value: boolean }>();
  useEffect(() => {
    if (!visible || status !== 'open' || mergeable != null) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const load = async () => {
      try {
        const value = await fetchPRMergeability({ dir, remoteId, remote, number, signal: controller.signal });
        if (controller.signal.aborted) return;
        if (value != null) {
          setResolved({ key, value });
          return;
        }
      } catch {
        // Keep unknown distinct from a confirmed conflict, and retry while visible.
      }
      if (!controller.signal.aborted) timer = setTimeout(load, 15_000);
    };
    void load();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [dir, key, mergeable, number, remote, remoteId, status, visible]);
  return mergeable ?? (resolved?.key === key ? resolved.value : undefined);
}
