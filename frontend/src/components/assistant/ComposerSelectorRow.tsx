export type SessionTarget = 'current' | 'worktree';

export function TargetSelector({
  directory, worktreesSupported, target = 'worktree', onTargetChange, disabled,
}: {
  directory?: string;
  worktreesSupported: boolean;
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
        </select>
      </div>
    </div>
  );
}
