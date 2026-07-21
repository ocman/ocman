import { useCallback, useRef, useState } from 'react';
import type { MutableRefObject } from 'react';
import type { TmuxState } from '../../lib/useTmux';
import type { TmuxSession } from '../../lib/api';
import { remoteLog } from '../../lib/remoteLog';
import { launchAndWait } from '../../lib/launchAndWait';
import { useClickOutside } from '../../lib/useClickOutside';

/**
 * Coordinates of the floating tmux-client picker, in viewport pixels.
 * Stored as state so the picker can re-render when re-positioned.
 */
interface PickerPosition {
  top: number;
  left: number;
}

/**
 * Public surface of useTmuxActions.
 *
 * The hook bundles the per-session tmux integration: matching the
 * current directory against running tmux sessions, switching the
 * active client, launching `opencode` inside a tmux pane, plus the
 * floating picker that lets a remote user pick which terminal to
 * jump to when they have multiple clients attached.
 */
export interface UseTmuxActionsResult {
  /** The tmux session whose resolvedPath matches the current directory, if any. */
  matchingTmuxSession: TmuxSession | undefined;
  /**
   * Tmux session name awaiting a client selection. `null` when the
   * picker isn't open. Used by the renderer to decide whether to
   * draw the picker.
   */
  pendingTmuxSession: string | null;
  /** Absolute viewport position for the picker (anchored to the
   *  click target). `null` while the picker is closed. */
  pickerPos: PickerPosition | null;
  /** Ref the picker element attaches to so click-outside can detect
   *  taps that should dismiss it. */
  pickerRef: MutableRefObject<HTMLDivElement | null>;
  /**
   * Click handler for the per-session "switch tmux" affordance.
   * Routes the request based on tmux topology:
   *   - single client (local or remote): switch with that client;
   *   - multiple clients: open the picker so the user can pick
   *     which tty to send the switch to.
   */
  handleTmuxSwitch: (e: React.MouseEvent, tmuxSessionName: string) => void;
  /** Picker callback that finalises the switch with the chosen client. */
  handleClientSelect: (clientTTY: string) => void;
  /**
   * Run `opencode --port 0` inside the tmux session for the page's
   * directory. Idempotent for the lifetime of one click — the
   * wrapped state guards against double-launch.
   */
  handleLaunchOpencode: () => Promise<void>;
  /** Whether a launch is in flight (button shows a spinner). */
  launchingOpencode: boolean;
  /**
   * Keyboard-shortcut entry point for the "switch to tmux" command.
   * Same routing as `handleTmuxSwitch` but with a fixed picker
   * position (top of viewport) since there's no anchor element.
   */
  handleTmuxShortcut: () => void;
}

/**
 * useTmuxActions owns the floating picker, the matching-session
 * lookup, the launch-opencode flow, and the keyboard shortcut entry
 * point that all want to switch the user's tmux client to a session
 * tied to the current page.
 *
 * `tmux` is the underlying TmuxState from useTmux(); the page passes
 * it through so a single useTmux subscription drives both the
 * palette command dispatch and these action helpers.
 */
export function useTmuxActions(
  tmux: TmuxState,
  directory: string | undefined,
  onLaunchError?: (message: string) => void,
  waitDeps?: {
    /** Re-fetch session state so liveConnection reflects the new instance. */
    reload: () => Promise<void>;
    /** Read the current live-connection status (latest value). */
    isLive: () => boolean;
  },
): UseTmuxActionsResult {
  const [pendingTmuxSession, setPendingTmuxSession] = useState<string | null>(null);
  const [pickerPos, setPickerPos] = useState<PickerPosition | null>(null);
  const pickerRef = useRef<HTMLDivElement | null>(null);

  useClickOutside(pickerRef, !!pendingTmuxSession, () => setPendingTmuxSession(null));

  const matchingTmuxSession = directory ? tmux.findSession(directory) : undefined;

  const handleTmuxSwitch = useCallback((e: React.MouseEvent, tmuxSessionName: string) => {
    // Single client: route directly to it.
    if (tmux.clients.length === 1) {
      tmux.switchSession(tmuxSessionName, tmux.clients[0].tty)
        .catch((err) => remoteLog.error('tmux switch failed', err));
      return;
    }
    // Multiple clients: open the picker anchored to the click target
    // so the user can choose which terminal to switch.
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setPickerPos({ top: rect.bottom + 4, left: rect.right });
    setPendingTmuxSession(tmuxSessionName);
  }, [tmux]);

  const handleClientSelect = useCallback((clientTTY: string) => {
    if (!pendingTmuxSession) return;
    tmux.switchSession(pendingTmuxSession, clientTTY)
      .catch((err) => remoteLog.error('tmux switch failed', err));
    setPendingTmuxSession(null);
  }, [pendingTmuxSession, tmux]);

  const [launchingOpencode, setLaunchingOpencode] = useState(false);
  const handleLaunchOpencode = useCallback(async () => {
    if (!directory) {
      onLaunchError?.('This session has no project directory, so OpenCode can\u2019t be launched for it.');
      return;
    }
    if (!tmux.available || launchingOpencode) return;
    setLaunchingOpencode(true);
    try {
      if (waitDeps) {
        // Launch, then poll until the new instance is reachable so the
        // composer re-enables on its own. Progress shows in the overlay.
        await launchAndWait(directory, {
          launch: tmux.launchOpencode,
          reload: waitDeps.reload,
          isLive: waitDeps.isLive,
        });
      } else {
        await tmux.launchOpencode(directory);
      }
    } catch (e) {
      remoteLog.error('Failed to launch opencode in tmux', e);
      // launchAndWait already reports into the progress overlay; only
      // surface via the toast fallback when we didn't drive the overlay.
      if (!waitDeps) {
        onLaunchError?.(e instanceof Error ? e.message : 'Failed to launch OpenCode in tmux.');
      }
    } finally {
      setLaunchingOpencode(false);
    }
  }, [launchingOpencode, directory, tmux, onLaunchError, waitDeps]);

  const handleTmuxShortcut = useCallback(() => {
    if (!matchingTmuxSession) return;
    // Single client: route directly to it.
    if (tmux.clients.length === 1) {
      tmux.switchSession(matchingTmuxSession.name, tmux.clients[0].tty)
        .catch((err) => remoteLog.error('tmux switch failed', err));
      return;
    }
    // Multiple clients: no anchor element — pin the picker near the
    // top-right of the viewport instead.
    setPickerPos({ top: 88, left: Math.min(window.innerWidth - 24, 420) });
    setPendingTmuxSession(matchingTmuxSession.name);
  }, [matchingTmuxSession, tmux]);

  return {
    matchingTmuxSession,
    pendingTmuxSession,
    pickerPos,
    pickerRef,
    handleTmuxSwitch,
    handleClientSelect,
    handleLaunchOpencode,
    launchingOpencode,
    handleTmuxShortcut,
  };
}
