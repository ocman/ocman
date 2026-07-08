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
 * Progress is reported to the global launchProgressStore so the
 * LaunchProgressOverlay can show the user which step is running
 * (launch tmux → wait for opencode → create session) no matter which
 * surface triggered the call. Callers with their own progress UI can
 * opt out via `reportProgress: false`.
 */

import { remoteLog } from './remoteLog';
import {
  launchProgressReporter,
  noopLaunchProgressReporter,
  type LaunchProgressReporter,
} from './launchProgressStore';

export interface CreateSessionWithLaunchDeps {
  createSession: (directory: string, platform?: string, title?: string) => Promise<{ id: string }>;
  launchOpencodeInTmux: (directory: string, remoteId?: string) => Promise<{ session: string }>;
  tmuxAvailable: boolean;
}

export interface CreateSessionWithLaunchOptions {
  directory: string;
  fallbackDirectory?: string;
  platform?: string;
  remoteId?: string;
  title?: string;
  /**
   * When true, the caller has already launched opencode in the target
   * directory (e.g. the /wt flow does this via the worktree backend).
   * createSessionWithLaunch will skip the `launchOpencodeInTmux` step
   * but still retry on `unreachable` while the freshly-spawned opencode
   * binds its port and gets picked up by the backend's lsof scan.
   */
  alreadyLaunched?: boolean;
  /**
   * When false, suppress reporting to the global launch-progress
   * overlay. Used by callers that render their own step-by-step
   * progress UI (WorktreeFormModal).
   */
  reportProgress?: boolean;
}

// Retry loop parameters. After launching opencode we poll the create
// endpoint — opencode needs a moment to bind a port. Local boots bind
// well under a second, so poll fast up front and back off; total budget
// stays ~9.5 s for slower machines.
const RETRY_DELAYS_MS = [400, 600, 800, 1200, 1500, 2000, 3000];

// Remote launches add gRPC round-trips plus a cold opencode boot on the
// remote machine, which routinely blows past the local ~9.5 s budget, so
// the hub times out even though opencode did come up remotely. Give the
// remote path a longer budget (~30 s).
// ponytail: doubled/extended schedule, tune if remote boots are slower.
const REMOTE_RETRY_DELAYS_MS = [2000, 3000, 3000, 4000, 4000, 4000, 4000, 4000];

function isUnreachable(err: unknown): boolean {
  return !!err && typeof err === 'object' && (err as { code?: string }).code === 'unreachable';
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function remoteIDForPlatform(platform?: string): string | undefined {
  if (!platform?.startsWith('r-')) return undefined;
  const end = platform.indexOf(':');
  return end > 2 ? platform.slice(2, end) : undefined;
}

export async function createSessionWithLaunch(
  deps: CreateSessionWithLaunchDeps,
  opts: CreateSessionWithLaunchOptions,
): Promise<{ id: string; directory?: string }> {
  const { createSession, launchOpencodeInTmux, tmuxAvailable } = deps;
  const { directory, fallbackDirectory, platform, title, alreadyLaunched } = opts;
  const remoteId = opts.remoteId || remoteIDForPlatform(platform);
  const isRemote = !!remoteId && remoteId !== 'local';
  const retryDelays = isRemote ? REMOTE_RETRY_DELAYS_MS : RETRY_DELAYS_MS;
  const progress: LaunchProgressReporter =
    opts.reportProgress === false ? noopLaunchProgressReporter : launchProgressReporter;

  remoteLog.info('setting up new project session', {
    directory,
    target: isRemote ? 'remote' : 'local',
    remoteId: isRemote ? remoteId : undefined,
    platform,
  });

  function launchInTmux(targetDirectory: string): Promise<{ session: string }> {
    return remoteId && remoteId !== 'local'
      ? launchOpencodeInTmux(targetDirectory, remoteId)
      : launchOpencodeInTmux(targetDirectory);
  }

  async function retryCreate(targetDirectory: string, err: unknown): Promise<{ id: string }> {
    let lastErr: unknown = err;
    const maxAttempts = retryDelays.length;
    for (let i = 0; i < maxAttempts; i++) {
      progress.step('wait');
      progress.attempt(i + 1, maxAttempts);
      await sleep(retryDelays[i]);
      progress.step('create');
      try {
        const res = await createSession(targetDirectory, platform, title);
        progress.succeed();
        return res;
      } catch (retryErr) {
        lastErr = retryErr;
        // Keep retrying only while we're still seeing "unreachable".
        // A different error (e.g. auth, validation) means opencode is
        // up but the request can't succeed — don't spin.
        if (!isUnreachable(retryErr)) break;
      }
    }
    progress.fail(
      isUnreachable(lastErr)
        ? 'OpenCode did not start in time. Check the tmux window for errors.'
        : errorMessage(lastErr),
    );
    throw lastErr;
  }

  try {
    return await createSession(directory, platform, title);
  } catch (err) {
    if (!isUnreachable(err)) throw err;
    // We retry-on-unreachable in two situations:
    //   - tmux is available and we can launch opencode ourselves
    //   - the caller already launched opencode and just wants us to
    //     wait until the lsof scan + port bind catch up
    if (!tmuxAvailable && !alreadyLaunched) {
      // We can't auto-launch (tmux unavailable) and nobody launched
      // opencode for us, so the session genuinely can't be created.
      // Surface it in the overlay rather than failing silently — the
      // command-palette caller otherwise only logs the error.
      progress.begin(directory, { skipLaunch: true });
      progress.fail(
        'No running OpenCode instance in this directory, and ocman can\'t start one ' +
          'because tmux is not on its PATH. After a reboot, ocman is often launched ' +
          'with a minimal PATH that omits tmux/opencode (e.g. homebrew or mise). ' +
          'Fix: start opencode manually here, or restart ocman from a login shell so ' +
          'it inherits your full PATH.',
      );
      throw err;
    }

    progress.begin(directory, { skipLaunch: !!alreadyLaunched });

    if (!alreadyLaunched) {
      progress.step('launch');
      try {
        await launchInTmux(directory);
      } catch (launchErr) {
        if (fallbackDirectory && fallbackDirectory !== directory) {
          remoteLog.error('Failed to launch opencode in tmux', launchErr);

          progress.begin(fallbackDirectory);
          try {
            const res = await createSession(fallbackDirectory, platform, title);
            progress.succeed();
            return { ...res, directory: fallbackDirectory };
          } catch (fallbackErr) {
            if (!isUnreachable(fallbackErr)) {
              progress.fail(errorMessage(fallbackErr));
              throw fallbackErr;
            }
          }

          progress.step('launch');
          try {
            await launchInTmux(fallbackDirectory);
          } catch (fallbackLaunchErr) {
            progress.fail('Failed to launch opencode in tmux.');
            remoteLog.error('Failed to launch opencode in fallback tmux directory', fallbackLaunchErr);
            throw err;
          }

          const res = await retryCreate(fallbackDirectory, err);
          return { ...res, directory: fallbackDirectory };
        }

        progress.fail('Failed to launch opencode in tmux.');
        // Fall through to rethrow the original "unreachable" error so
        // the UI's error message still matches what the user requested.
        remoteLog.error('Failed to launch opencode in tmux', launchErr);
        throw err;
      }
    }

    return retryCreate(directory, err);
  }
}
