import { useEffect, useState, useCallback } from 'react';
import { api } from '../../lib/api';
import { useUiStore } from '../../lib/uiStore';

/**
 * useBranches loads the local branch list for a directory (current
 * branch first) and exposes a checkout action. Shared by BranchSelector;
 * kept tiny so both the composer footer and any future consumer can reuse
 * the fetch + checkout plumbing without duplicating error handling.
 */
function useBranches(directory?: string) {
  const [branches, setBranches] = useState<string[]>([]);
  const [current, setCurrent] = useState<string>('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(
    (signal?: AbortSignal) => {
      if (!directory) return;
      api
        .gitBranches(directory, signal)
        .then((res) => {
          if (signal?.aborted) return;
          setBranches(res.branches);
          setCurrent(res.branches[0] ?? '');
          setError(null);
        })
        .catch((err: unknown) => {
          if (signal?.aborted) return;
          setError(err instanceof Error ? err.message : 'branch list failed');
        });
    },
    [directory],
  );

  useEffect(() => {
    const c = new AbortController();
    refresh(c.signal);
    return () => c.abort();
  }, [refresh]);

  const checkout = useCallback(
    async (branch: string) => {
      if (!directory || branch === current) return;
      setBusy(true);
      setError(null);
      try {
        await api.gitCheckout(directory, branch);
        setCurrent(branch);
        refresh();
      } catch (err) {
        setError(err instanceof Error ? err.message : 'checkout failed');
      } finally {
        setBusy(false);
      }
    },
    [directory, current, refresh],
  );

  return { branches, current, busy, error, checkout };
}

/**
 * BranchSelector is the git-branch switcher shown in the composer footer
 * (left of the cost indicator). Selecting a branch runs `git checkout`
 * via /api/git/checkout; a dirty tree comes back as a 409 whose message
 * is surfaced inline. Renders nothing for a non-repo directory.
 */
export function BranchSelector({ directory }: { directory?: string }) {
  const { branches, current, busy, error, checkout } = useBranches(directory);

  if (!directory || branches.length === 0) return null;

  return (
    <span className="oc-branch-selector" data-testid="composer-branch-selector">
      {error && (
        <span className="oc-selector-error" role="alert" title={error}>
          {error.length > 40 ? error.slice(0, 40) + '…' : error}
        </span>
      )}
      <span className="oc-selector-icon" aria-hidden="true">
        <i className="bi bi-git" />
      </span>
      <select
        className="oc-bar-select"
        aria-label="Git branch"
        disabled={busy}
        title="Switch branch (git checkout)"
        value={current}
        onChange={(e) => void checkout(e.target.value)}
      >
        {branches.map((b) => (
          <option key={b} value={b}>
            {b}
          </option>
        ))}
      </select>
    </span>
  );
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
}: {
  directory?: string;
  worktreesSupported: boolean;
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
              openWorktreeForm({ projectDir: directory, branch: current });
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
