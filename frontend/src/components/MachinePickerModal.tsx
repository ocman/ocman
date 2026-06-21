import { useEffect, useState } from 'react';
import './MachinePickerModal.css';
import {
  subscribeMachinePicker,
  resolveMachineChoice,
  type MachinePickerState,
} from '../lib/machinePicker';
import type { TargetCandidate } from '../lib/api.types';

/**
 * MachinePickerModal renders the new-session machine picker (AD-15).
 * Mounted once at the app root; it shows when resolveTargetForDir needs
 * the operator to choose a machine (project exists on several hosts, or
 * on none). Choosing a row resolves the pending promise with that
 * machine's platform key.
 */
export function MachinePickerModal() {
  const [state, setState] = useState<MachinePickerState>({
    open: false, dir: '', candidates: [], remotes: [],
  });

  useEffect(() => subscribeMachinePicker(setState), []);

  useEffect(() => {
    if (!state.open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') resolveMachineChoice(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [state.open]);

  if (!state.open) return null;

  const hasMatches = state.candidates.length > 0;
  // Zero matches: offer all enabled remotes (plus the local machine) so
  // the operator can still start the project somewhere.
  const options: TargetCandidate[] = hasMatches
    ? state.candidates
    : [{ remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: state.dir }, ...state.remotes];

  return (
    <div
      className="machine-picker-backdrop"
      data-testid="machine-picker-backdrop"
      onClick={() => resolveMachineChoice(null)}
    >
      <div
        className="machine-picker"
        role="dialog"
        aria-label="Choose a machine"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="machine-picker-title">
          {hasMatches ? 'Choose a machine' : 'Project not found on any machine'}
        </div>
        <div className="machine-picker-desc">
          {hasMatches
            ? 'This project exists on more than one machine. Where should the session run?'
            : 'Pick a machine to start the session on.'}
        </div>
        <ul className="machine-picker-list">
          {options.map((c) => (
            <li key={c.platform + c.remoteId}>
              <button
                type="button"
                className="machine-picker-option"
                onClick={() => resolveMachineChoice(c.platform)}
              >
                <span className="machine-picker-name">{c.remoteName}</span>
                {c.dir && <span className="machine-picker-dir mono">{c.dir}</span>}
              </button>
            </li>
          ))}
        </ul>
        <button type="button" className="machine-picker-cancel" onClick={() => resolveMachineChoice(null)}>
          Cancel
        </button>
      </div>
    </div>
  );
}
