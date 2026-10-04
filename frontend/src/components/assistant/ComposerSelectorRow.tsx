import { SearchSelect } from '../SearchSelect';

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
  const options = [
    { value: 'current', label: 'Current checkout' },
    ...(worktreesSupported ? [
      { value: 'worktree', label: 'New worktree' },
      ...worktrees.map((tree) => ({
        value: `dir:${tree.path}`,
        label: `Worktree ${tree.branch || tree.path.split('/').pop()}`,
        section: 'Existing worktrees',
      })),
    ] : []),
  ];
  return (
    <div className="oc-composer-selectors" data-testid="composer-target-selector">
      <div className="oc-composer-selectors-left">
        <span className="oc-selector-icon" aria-hidden="true"><i className="bi bi-folder" /></span>
        <SearchSelect className="oc-bar-search" ariaLabel="Session target" title="Where to start this conversation"
          placeholder="Current checkout" searchLabel="Search worktrees" options={options}
          disabled={disabled} value={worktreesSupported ? target : 'current'}
          onChange={(value) => onTargetChange?.(value as SessionTarget)} />
      </div>
    </div>
  );
}
