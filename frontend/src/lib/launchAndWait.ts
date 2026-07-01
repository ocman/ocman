import { launchProgressReporter, type LaunchProgressReporter } from './launchProgressStore';

/**
 * Poll cadence after launching opencode: the backend lsof cache has a
 * TTL (~3 s) and opencode needs a moment to boot + bind its port, so we
 * give it up to ~9.5 s before giving up. Mirrors createSessionWithLaunch.
 */
const RETRY_DELAYS_MS = [1500, 2000, 2000, 2000, 2000];

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export interface LaunchAndWaitDeps {
  /** Launch `opencode --port 0` in tmux for the directory. */
  launch: (directory: string) => Promise<unknown>;
  /** Re-fetch session state so liveConnection reflects the new instance. */
  reload: () => Promise<void>;
  /** Read current live-connection status after a reload. */
  isLive: () => boolean;
  /** Progress reporter (defaults to the global overlay store). */
  progress?: LaunchProgressReporter;
  /** Injectable sleep for tests. */
  wait?: (ms: number) => Promise<void>;
}

/**
 * Launch opencode in tmux for `directory`, then poll until the session's
 * live connection comes up, driving the LaunchProgressOverlay so the user
 * sees progress. On success the caller's liveConnection mirror flips and
 * the composer re-enables automatically. Throws on launch failure or if
 * the instance never becomes reachable within the retry budget.
 */
export async function launchAndWait(
  directory: string,
  deps: LaunchAndWaitDeps,
): Promise<void> {
  const progress = deps.progress ?? launchProgressReporter;
  const wait = deps.wait ?? sleep;

  progress.begin(directory, { skipLaunch: false });
  progress.step('launch');
  try {
    await deps.launch(directory);
  } catch (e) {
    progress.fail(e instanceof Error ? e.message : 'Failed to launch OpenCode in tmux.');
    throw e;
  }

  progress.step('wait');
  const maxAttempts = RETRY_DELAYS_MS.length;
  for (let i = 0; i < maxAttempts; i++) {
    progress.attempt(i + 1, maxAttempts);
    await wait(RETRY_DELAYS_MS[i]);
    await deps.reload();
    if (deps.isLive()) {
      progress.succeed();
      return;
    }
  }
  progress.fail(
    'OpenCode was launched but did not become reachable in time. ' +
      'Check the tmux window for errors, then reload the page.',
  );
  throw new Error('opencode did not become reachable after launch');
}
