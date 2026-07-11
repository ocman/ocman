import { useCallback, useState } from 'react';
import { useApiStore } from './apiStore';
import type { WorkingTreeDiff } from './api';
import {
  useDebouncedSessionResource,
  type DebouncedSessionResourceOptions,
} from './useDebouncedSessionResource';

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
  /**
   * Manually trigger an immediate refetch, bypassing the debounce.
   * Wired to the user-facing "Refresh" button. Safe to call when
   * `enabled` is false (no-op).
   */
  refresh: () => void;
}

export type UseWorkingTreeDiffOptions = DebouncedSessionResourceOptions;

// A 404 (or "not a git worktree" body) means the directory isn't a git
// repo. gitDiff throws a plain Error whose message is the response text
// (see throwForStatus in api.ts), so we sniff the message.
function isNotRepoError(err: unknown): boolean {
  const message = err instanceof Error ? err.message : '';
  return message.includes('404') || message.toLowerCase().includes('not a git worktree');
}

/**
 * useWorkingTreeDiff fetches /api/git/diff for the given absolute
 * directory and re-fetches on dirtyTick changes. It's a thin wrapper
 * around useDebouncedSessionResource, keying on `dir` instead of a
 * session id (the base hook treats the key as an opaque reset trigger).
 *
 * The hook distinguishes "directory isn't a git worktree" (HTTP 404)
 * from a real error: the fetch worker catches that case, flips the
 * `notRepo` flag, and resolves with the empty diff so the base hook
 * reports `error: null`. Consumers can then render a friendly empty
 * state for non-repo directories rather than a generic error toast.
 */
export function useWorkingTreeDiff(
  dir: string | undefined,
  options: UseWorkingTreeDiffOptions = {},
): UseWorkingTreeDiffResult {
  const [notRepo, setNotRepo] = useState(false);
  const getGitDiff = useApiStore((s) => s.getGitDiff);

  const fetchDiff = useCallback(
    async (id: string, signal: AbortSignal): Promise<WorkingTreeDiff> => {
      // Clear the flag at the start of every fetch (covers dir changes
      // and refreshes) so a stale notRepo doesn't linger.
      setNotRepo(false);
      try {
        // fresh=true bypasses the backend's tiny cache so an
        // edit-event-triggered refresh is never sub-second stale.
        return await getGitDiff(id, { fresh: true }, signal);
      } catch (err) {
        if (isNotRepoError(err)) {
          setNotRepo(true);
          return EMPTY_DIFF;
        }
        throw err;
      }
    },
    [getGitDiff],
  );

  const { data, loading, error, refresh } = useDebouncedSessionResource(
    dir,
    fetchDiff,
    EMPTY_DIFF,
    'Failed to load diff',
    options,
  );

  return { data, loading, error, notRepo, refresh };
}
