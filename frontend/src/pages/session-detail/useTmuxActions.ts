import { useCallback, useEffect, useRef, useState } from 'react';
import type { MutableRefObject } from 'react';
import type { TmuxState } from '../../lib/useTmux';
import type { TmuxSession } from '../../lib/api';
import { remoteLog } from '../../lib/remoteLog';

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
   *   - local users: switch directly (server defaults the client to
   *     the user's tty);
   *   - remote single-client: switch with that client;
   *   - remote multi-client: open the picker so the user can pick
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
): UseTmuxActionsResult {
  const [pendingTmuxSession, setPendingTmuxSession] = useState<string | null>(null);
  const [pickerPos, setPickerPos] = useState<PickerPosition | null>(null);
  const pickerRef = useRef<HTMLDivElement | null>(null);

  // Close the picker when the user clicks anywhere outside it.
  useEffect(() => {
    if (!pendingTmuxSession) return;
    const handle = (e: MouseEvent) => {
      if (pickerRef.current && !pickerRef.current.contains(e.target as Node)) {
        setPendingTmuxSession(null);
      }
    };
    document.addEventListener('mousedown', handle);
    return () => document.removeEventListener('mousedown', handle);
  }, [pendingTmuxSession]);

  const matchingTmuxSession = directory ? tmux.findSession(directory) : undefined;

  const handleTmuxSwitch = useCallback((e: React.MouseEvent, tmuxSessionName: string) => {
    // Local user: fire directly, server defaults to /dev/ttys000.
    if (tmux.isLocal) {
      tmux.switchSession(tmuxSessionName).catch((err) => remoteLog.error('tmux switch failed', err));
      return;
    }
    // Remote user with single client: route to that client.
    if (tmux.clients.length === 1) {
      tmux.switchSession(tmuxSessionName, tmux.clients[0].tty)
        .catch((err) => remoteLog.error('tmux switch failed', err));
      return;
    }
    // Remote user with multiple clients: open the picker anchored to
    // the click target so the user can choose.
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
    if (!directory || !tmux.available || launchingOpencode) return;
    setLaunchingOpencode(true);
    try {
      await tmux.launchOpencode(directory);
    } catch (e) {
      remoteLog.error('Failed to launch opencode in tmux', e);
    } finally {
      setLaunchingOpencode(false);
    }
  }, [launchingOpencode, directory, tmux]);

  const handleTmuxShortcut = useCallback(() => {
    if (!matchingTmuxSession) return;
    if (tmux.isLocal) {
      tmux.switchSession(matchingTmuxSession.name)
        .catch((err) => remoteLog.error('tmux switch failed', err));
      return;
    }
    if (tmux.clients.length === 1) {
      tmux.switchSession(matchingTmuxSession.name, tmux.clients[0].tty)
        .catch((err) => remoteLog.error('tmux switch failed', err));
      return;
    }
    // Shortcut path has no anchor element — pin the picker near the
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
