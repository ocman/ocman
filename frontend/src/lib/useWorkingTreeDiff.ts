import { api } from './api';
import type { WorkingTreeDiff } from './api';
import { useSessionResourceQuery, type SessionResourceOptions } from './useSessionResourceQuery';

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
   * Refetch immediately. Wired to the user-facing "Refresh" button.
   * Safe to call when `enabled` is false (no-op).
   */
  refresh: () => void;
}

export interface UseWorkingTreeDiffOptions extends SessionResourceOptions {
  // The session's owning machine. Without it the backend infers the owner
  // from the path, which falls back to the hub for an unmapped remote dir.
  remoteId?: string;
}

// A 404 (or "not a git worktree" body) means the directory isn't a git
// repo. gitDiff throws a plain Error whose message is the response text
// (see throwForStatus in api.ts), so we sniff the message.
function isNotRepoError(err: unknown): boolean {
  const message = err instanceof Error ? err.message : '';
  return message.includes('404') || message.toLowerCase().includes('not a git worktree');
}

/**
 * useWorkingTreeDiff fetches /api/git/diff for the given absolute
 * directory and re-fetches on dirtyTick changes.
 *
 * "Directory isn't a git worktree" (HTTP 404) is not an error: it sets
 * `notRepo` and yields the empty diff, so consumers can render a
 * friendly empty state for non-repo directories.
 */
export function useWorkingTreeDiff(
  dir: string | undefined,
  { remoteId, ...options }: UseWorkingTreeDiffOptions = {},
): UseWorkingTreeDiffResult {
  const getGitDiff = api.gitDiff;
  const { data, loading, error, refresh } = useSessionResourceQuery(
    dir ? ['git-diff', remoteId ?? '', dir] : undefined,
    async (signal) => {
      try {
        // fresh=true bypasses the backend's tiny cache so an
        // edit-event-triggered refresh is never sub-second stale.
        return { diff: await getGitDiff(dir!, remoteId ? { fresh: true, remoteId } : { fresh: true }, signal), notRepo: false };
      } catch (err) {
        if (isNotRepoError(err)) return { diff: EMPTY_DIFF, notRepo: true };
        throw err;
      }
    },
    { diff: EMPTY_DIFF, notRepo: false },
    'Failed to load diff',
    options,
  );

  return { data: data?.diff ?? null, loading, error, notRepo: data?.notRepo ?? false, refresh };
}
