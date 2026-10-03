/** `dir:<path>` starts the conversation in an existing linked worktree. */
export type SessionTarget = 'current' | 'worktree' | `dir:${string}`;

export interface TargetWorktree { path: string; branch: string }

export function TargetSelector({
  directory, worktreesSupported, worktrees = [], target = 'worktree', onTargetChange, disabled,
}: {
  directory?: string;
  worktreesSupported: boolean;
  /** Existing linked worktrees offered as alternative targets. */
  worktrees?: TargetWorktree[];
  target?: SessionTarget;
  onTargetChange?: (target: SessionTarget) => void;
  disabled?: boolean;
}) {
  if (!directory) return null;
  return (
    <div className="oc-composer-selectors" data-testid="composer-target-selector">
      <div className="oc-composer-selectors-left">
        <span className="oc-selector-icon" aria-hidden="true"><i className="bi bi-folder" /></span>
        <select className="oc-bar-select" aria-label="Session target" title="Where to start this conversation"
          disabled={disabled} value={worktreesSupported ? target : 'current'}
          onChange={(e) => onTargetChange?.(e.target.value as SessionTarget)}>
          <option value="current">Current checkout</option>
          {worktreesSupported && <option value="worktree">New worktree</option>}
          {worktreesSupported && worktrees.map((tree) => (
            <option key={tree.path} value={`dir:${tree.path}`}>
              Worktree {tree.branch || tree.path.split('/').pop()}
            </option>
          ))}
        </select>
      </div>
    </div>
  );
}
