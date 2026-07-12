import { useEffect, useState } from 'react';
import { api } from '../../lib/api';
import { useUiStore } from '../../lib/uiStore';

function useBranches(directory?: string) {
  const [branches, setBranches] = useState<string[]>([]);
  const [current, setCurrent] = useState<string>('');

  useEffect(() => {
    const c = new AbortController();
    if (directory) {
      api.gitBranches(directory, c.signal).then((res) => {
        if (c.signal.aborted) return;
        setBranches(res.branches);
        setCurrent(res.branches[0] ?? '');
      }).catch(() => {});
    }
    return () => c.abort();
  }, [directory]);

  return { branches, current };
}

/**
 * TargetSelector is the "Current checkout / New worktree…" target select,
 * shown below the composer *only for a new conversation* (the caller
 * gates on that; once a session has messages the target is fixed and this
 * isn't rendered at all). Choosing "New worktree…" opens the existing
 * worktree-create form prefilled with the current branch.
 *
 * It resolves the current branch itself (cheap, cached server-side) so the
 * worktree form can be prefilled; it renders nothing for a non-repo dir.
 */
export function TargetSelector({
  directory,
  worktreesSupported,
  parentSessionId,
}: {
  directory?: string;
  worktreesSupported: boolean;
  /** Current session, if any; the new worktree inherits its permissions (#101). */
  parentSessionId?: string;
}) {
  const openWorktreeForm = useUiStore((s) => s.openWorktreeForm);
  const { branches, current } = useBranches(directory);

  if (!directory || branches.length === 0) return null;

  return (
    <div className="oc-composer-selectors" data-testid="composer-target-selector">
      <div className="oc-composer-selectors-left">
        <span className="oc-selector-icon" aria-hidden="true">
          <i className="bi bi-folder" />
        </span>
        <select
          className="oc-bar-select"
          aria-label="Session target"
          title="Where to start this conversation"
          value="current"
          onChange={(e) => {
            if (e.target.value === 'worktree') {
              openWorktreeForm({ projectDir: directory, branch: current, parentSessionId });
              e.target.value = 'current';
            }
          }}
        >
          <option value="current">Current checkout</option>
          {worktreesSupported && <option value="worktree">New worktree…</option>}
        </select>
      </div>
    </div>
  );
}
