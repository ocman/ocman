import { useEffect, useState } from 'react';
import './MachinePickerModal.css';
import {
  subscribeMachinePicker,
  resolveMachineChoice,
  type MachinePickerState,
} from '../lib/machinePicker';
import type { TargetCandidate } from '../lib/api.types';
import { Modal } from './Modal';

/**
 * MachinePickerModal renders the new-session machine picker (AD-15).
 * Mounted once at the app root; it shows when resolveTargetForDir needs
 * the operator to choose a machine (project exists on several hosts, or
 * on none). Choosing a row resolves the pending promise with that machine.
 */
export function MachinePickerModal() {
  const [state, setState] = useState<MachinePickerState>({
    open: false, dir: '', candidates: [], remotes: [],
  });

  useEffect(() => subscribeMachinePicker(setState), []);

  if (!state.open) return null;

  const hasMatches = state.candidates.length > 0;
  // Zero matches: offer all enabled remotes (plus the local machine) so
  // the operator can still start the project somewhere.
  const options: TargetCandidate[] = hasMatches
    ? state.candidates
    : [{ remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: state.dir }, ...state.remotes];

  return (
    <Modal
      backdropClassName="machine-picker-backdrop"
      backdropTestId="machine-picker-backdrop"
      dialogClassName="machine-picker"
      label="Choose a machine"
      onClose={() => resolveMachineChoice(null)}
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
              onClick={() => resolveMachineChoice({ platform: c.platform, remoteId: c.remoteId })}
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
    </Modal>
  );
}
