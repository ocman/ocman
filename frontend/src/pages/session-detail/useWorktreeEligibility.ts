import { useEffect, useState } from 'react';
import { fetchJSON, type GitInfo, type WorktreeEntry } from '../../lib/api';
import type { TargetWorktree } from '../../components/assistant/ComposerSelectorRow';

export interface WorktreeEligibility {
  /** Whether a new conversation here may start in a fresh worktree. */
  canCreate: boolean;
  worktrees: TargetWorktree[];
}

/**
 * Whether a new conversation in `directory` may start in a fresh worktree:
 * the directory is a repository and is not itself already a linked
 * worktree. Both reads only need the owner; they are fetched together.
 */
export function useWorktreeEligibility(directory: string, remoteId: string) {
  const [resolved, setResolved] = useState<WorktreeEligibility>();
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a directory/owner change restarts the lookup.
    setResolved(undefined);
    setError('');
    void (async () => {
      try {
        const query = new URLSearchParams({ dir: directory, remoteId });
        // The list is ignored (and may fail) when the directory is not a repository.
        const list = fetchJSON<{ worktrees: WorktreeEntry[] }>(`/api/worktree/list?${query}`, controller.signal);
        list.catch(() => undefined);
        const info = await fetchJSON<Record<string, GitInfo>>(`/api/git/info?${query}`, controller.signal);
        const repo = !!info[directory]?.branch;
        let alreadyChosen = false;
        let linked: TargetWorktree[] = [];
        if (repo) {
          const { worktrees } = await list;
          // Read the owner's actual workspaces, so manual selection survives reloads
          // and also covers worktrees outside ocman's managed directory layout.
          alreadyChosen = worktrees.some((tree) => !tree.main &&
            (directory === tree.path || directory.startsWith(`${tree.path}/`)));
          linked = worktrees.filter((tree) => !tree.main && !tree.bare)
            .map((tree) => ({ path: tree.path, branch: tree.branch }));
        }
        if (!controller.signal.aborted) setResolved({ canCreate: repo && !alreadyChosen, worktrees: linked });
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err));
      }
    })();
    return () => controller.abort();
  }, [directory, remoteId, attempt]);

  return { resolved, error, retry: () => { setError(''); setAttempt((value) => value + 1); } };
}
