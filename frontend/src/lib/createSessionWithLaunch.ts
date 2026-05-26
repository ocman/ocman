/**
 * createSessionWithLaunch wraps `createSession` with an auto-launch
 * fallback for the common "I clicked 'new session' but no opencode is
 * running in that directory" case.
 *
 * Flow:
 *   1. Call `createSession`.
 *   2. If it rejects with code === 'unreachable' AND tmux is available,
 *      launch `opencode --port 0` in a new tmux window via
 *      `launchOpencodeInTmux`, then retry the createSession call a few
 *      times while the new opencode process comes up and starts
 *      listening.
 *   3. The /wt flow has already launched opencode in a tmux window, so
 *      it sets `alreadyLaunched: true` to skip the launch step but
 *      still benefit from the retry loop while the new instance
 *      finishes booting and the lsof scan picks it up.
 *   4. If tmux isn't available AND the launch wasn't done externally,
 *      or the retry loop exhausts, or any other error occurs,
 *      re-throw.
 *
 * Callers supply a status callback so the UI can render
 * "Launching opencode…" while the retry loop runs.
 */

import { remoteLog } from './remoteLog';

export type LaunchStatus = 'idle' | 'launching' | 'retrying';

export interface CreateSessionWithLaunchDeps {
  createSession: (directory: string, platform?: string, title?: string) => Promise<{ id: string }>;
  launchOpencodeInTmux: (directory: string) => Promise<{ session: string }>;
  tmuxAvailable: boolean;
  onStatusChange?: (status: LaunchStatus) => void;
}

export interface CreateSessionWithLaunchOptions {
  directory: string;
  platform?: string;
  title?: string;
  /**
   * When true, the caller has already launched opencode in the target
   * directory (e.g. the /wt flow does this via the worktree backend).
   * createSessionWithLaunch will skip the `launchOpencodeInTmux` step
   * but still retry on `unreachable` while the freshly-spawned opencode
   * binds its port and gets picked up by the backend's lsof scan.
   */
  alreadyLaunched?: boolean;
}

// Retry loop parameters. After launching opencode we poll the create
// endpoint — the lsof cache has a 3 s TTL on the backend and opencode
// itself needs a moment to bind a port, so we give it up to ~9 s.
const RETRY_DELAYS_MS = [1500, 2000, 2000, 2000, 2000];

function isUnreachable(err: unknown): boolean {
  return !!err && typeof err === 'object' && (err as { code?: string }).code === 'unreachable';
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function createSessionWithLaunch(
  deps: CreateSessionWithLaunchDeps,
  opts: CreateSessionWithLaunchOptions,
): Promise<{ id: string }> {
  const { createSession, launchOpencodeInTmux, tmuxAvailable, onStatusChange } = deps;
  const { directory, platform, title, alreadyLaunched } = opts;

  try {
    return await createSession(directory, platform, title);
  } catch (err) {
    if (!isUnreachable(err)) throw err;
    // We retry-on-unreachable in two situations:
    //   - tmux is available and we can launch opencode ourselves
    //   - the caller already launched opencode and just wants us to
    //     wait until the lsof scan + port bind catch up
    if (!tmuxAvailable && !alreadyLaunched) throw err;

    if (!alreadyLaunched) {
      onStatusChange?.('launching');
      try {
        await launchOpencodeInTmux(directory);
      } catch (launchErr) {
        onStatusChange?.('idle');
        // Fall through to rethrow the original "unreachable" error so
        // the UI's error message still matches what the user requested.
        remoteLog.error('Failed to launch opencode in tmux', launchErr);
        throw err;
      }
    }

    onStatusChange?.('retrying');
    let lastErr: unknown = err;
    for (const delay of RETRY_DELAYS_MS) {
      await sleep(delay);
      try {
        const res = await createSession(directory, platform, title);
        onStatusChange?.('idle');
        return res;
      } catch (retryErr) {
        lastErr = retryErr;
        // Keep retrying only while we're still seeing "unreachable".
        // A different error (e.g. auth, validation) means opencode is
        // up but the request can't succeed — don't spin.
        if (!isUnreachable(retryErr)) break;
      }
    }
    onStatusChange?.('idle');
    throw lastErr;
  }
}
