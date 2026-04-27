import { useEffect, useRef, useState } from 'react';
import { useApiStore } from './apiStore';
import type { WorkingTreeDiff } from './api';

// Mirrors useSessionChanges: SSE-driven refetch via a dirtyTick
// counter, debounced 500 ms. The two hooks share a debounce window
// because they're often refreshed by the same event (an agent's
// edit/write tool call) and you want them to refresh in lockstep.
const REFETCH_DEBOUNCE_MS = 500;

const EMPTY_DIFF: WorkingTreeDiff = {
  repo: '',
  branch: '',
  ahead: 0,
  behind: 0,
  files: [],
  truncated: false,
};

export interface UseWorkingTreeDiffResult {
  data: WorkingTreeDiff | null;
  loading: boolean;
  error: string | null;
  notRepo: boolean;
}

export interface UseWorkingTreeDiffOptions {
  enabled?: boolean;
  // Increments to request a fresh fetch (mirrors useSessionChanges).
  dirtyTick?: number;
}

/**
 * useWorkingTreeDiff fetches /api/git/diff for the given absolute
 * directory and re-fetches on dirtyTick changes (debounced).
 *
 * The hook distinguishes "directory isn't a git worktree" (HTTP 404)
 * from a real error; consumers can render a friendly empty state for
 * non-repo directories rather than a generic error toast.
 */
export function useWorkingTreeDiff(
  dir: string | undefined,
  { enabled = true, dirtyTick = 0 }: UseWorkingTreeDiffOptions = {},
): UseWorkingTreeDiffResult {
  const [data, setData] = useState<WorkingTreeDiff | null>(null);
  const [loading, setLoading] = useState(enabled && !!dir);
  const [error, setError] = useState<string | null>(null);
  const [notRepo, setNotRepo] = useState(false);

  const getGitDiff = useApiStore((s) => s.getGitDiff);

  const lastDirRef = useRef<string | undefined>(undefined);
  const timerRef = useRef<number | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!enabled || !dir) {
      setData(enabled ? null : EMPTY_DIFF);
      setLoading(false);
      setError(null);
      setNotRepo(false);
      return;
    }

    if (lastDirRef.current !== dir) {
      setData(null);
      setLoading(true);
      setError(null);
      setNotRepo(false);
      lastDirRef.current = dir;
    }

    const fetchNow = () => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;

      // The frontend always asks for fresh=true on SSE-driven
      // refetches; the backend's 1s cache still coalesces near-
      // simultaneous requests but we don't want to render
      // sub-second-stale data after an edit event.
      getGitDiff(dir, { fresh: true }, controller.signal)
        .then((resp) => {
          if (controller.signal.aborted) return;
          setData(resp);
          setLoading(false);
          setError(null);
          setNotRepo(false);
        })
        .catch((err: unknown) => {
          if (controller.signal.aborted) return;
          if (err instanceof Error && err.name === 'AbortError') return;
          // 404 = not a git worktree; surface as a separate flag
          // so the UI can show "this project isn't a git repo"
          // rather than an error.
          const message = err instanceof Error ? err.message : 'Failed to load diff';
          if (message.includes('404') || message.toLowerCase().includes('not a git worktree')) {
            setNotRepo(true);
            setError(null);
          } else {
            setError(message);
          }
          setLoading(false);
        });
    };

    if (data === null) {
      fetchNow();
    } else {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
      }
      timerRef.current = window.setTimeout(() => {
        timerRef.current = null;
        fetchNow();
      }, REFETCH_DEBOUNCE_MS);
    }

    return () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
      abortRef.current?.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dir, enabled, dirtyTick, getGitDiff]);

  return { data, loading, error, notRepo };
}
