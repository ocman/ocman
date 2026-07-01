/**
 * Whether the "Launch session" affordance (composer button / disconnected
 * toast) should be offered. All four conditions must hold, and — critically —
 * a directory is required: the backend launch endpoint rejects an empty or
 * relative path, and `handleLaunchOpencode` silently returns without one, so
 * offering the button without a directory renders a dead control.
 */
export function canLaunchSession(opts: {
  portAvailable: boolean;
  hasPendingPrompt: boolean;
  tmuxAvailable: boolean;
  liveConnectionHint: boolean;
  directory: string | undefined;
}): boolean {
  return (
    !opts.portAvailable &&
    !opts.hasPendingPrompt &&
    opts.tmuxAvailable &&
    !!opts.liveConnectionHint &&
    !!opts.directory
  );
}
