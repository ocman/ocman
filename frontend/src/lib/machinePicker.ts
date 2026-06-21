// machinePicker drives the new-session machine-picker flow (AD-15) for
// multi-remote support. resolveTargetForDir asks the hub which machines
// have a project checked out and decides whether a prompt is needed:
//
//   - exactly one candidate -> resolve immediately, no prompt
//   - >1 candidates or 0     -> open the MachinePicker modal and resolve
//                               with the operator's choice
//
// The modal is rendered once at the app root (MachinePickerModal) and
// subscribes to this module-level controller, mirroring the launch-
// progress pattern. The chosen `platform` (compound 'r-<id>:opencode'
// for a remote, 'opencode' for local) is fed to createSession.

import { api } from './api';
import type { TargetCandidate } from './api.types';

export interface MachinePickerState {
  open: boolean;
  dir: string;
  candidates: TargetCandidate[];
  remotes: TargetCandidate[];
}

type Listener = (s: MachinePickerState) => void;

let state: MachinePickerState = { open: false, dir: '', candidates: [], remotes: [] };
const listeners = new Set<Listener>();
let resolver: ((platform: string | null) => void) | null = null;

function emit() {
  for (const fn of listeners) fn(state);
}

export function subscribeMachinePicker(fn: Listener): () => void {
  listeners.add(fn);
  fn(state);
  return () => listeners.delete(fn);
}

/** Operator chose a machine (or cancelled with null). Closes the modal. */
export function resolveMachineChoice(platform: string | null) {
  state = { open: false, dir: '', candidates: [], remotes: [] };
  emit();
  const r = resolver;
  resolver = null;
  r?.(platform);
}

/**
 * Resolves the target platform for creating a session in `dir`. Returns
 * the platform string to pass to createSession, or null if the operator
 * cancelled. Auto-resolves without a prompt when there is exactly one
 * candidate machine; otherwise opens the picker modal.
 */
export async function resolveTargetForDir(dir: string): Promise<string | null> {
  let resp;
  try {
    resp = await api.resolveTargets(dir);
  } catch {
    // Resolver unavailable (e.g. single-host without the endpoint):
    // fall back to the local default platform.
    return '';
  }
  const { candidates, remotes } = resp;
  if (candidates.length === 1) {
    return candidates[0].platform;
  }
  // 0 or >1: prompt the operator.
  return new Promise<string | null>((resolve) => {
    resolver = resolve;
    state = { open: true, dir, candidates, remotes };
    emit();
  });
}
