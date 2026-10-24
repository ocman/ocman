import { useEffect, useRef } from 'react';
import type { NotifyEntry } from './api';
import { useNotifySnapshot, type NotifySnapshot } from './useNotifyData';

/**
 * Produce a stable string key from a session's notification-relevant
 * state. Shared by the bell, OS-notification, and in-app-toast hooks so
 * they dedupe on identical criteria (status + pending prompt flags).
 */
export function notifyStateKey(
  s: { status: string; pendingPermission?: boolean; pendingQuestion?: boolean },
): string {
  return `${s.status}|${s.pendingPermission ? '1' : '0'}|${s.pendingQuestion ? '1' : '0'}`;
}

export type NotifyBaselineOptions = {
  /** Per-session state compared against the baseline; defaults to notifyStateKey. */
  key?: (s: NotifyEntry) => string;
  /**
   * When data already exists at mount, snapshot it as the baseline only if
   * the tab is hidden (`'hidden'`, default), always, or never.
   */
  initialBaseline?: 'hidden' | 'always' | 'never';
  /** Check existing data at mount and again whenever the tab becomes visible. */
  checkWhenShown?: boolean;
  /** Keep the baseline across visibility changes. */
  ignoreVisibility?: boolean;
  /** Clear extra caller state when the tab becomes visible. */
  onVisibleReset?: () => void;
};

type CheckFn = (sessions: NotifyEntry[], baseline: Map<string, string> | null) => boolean | void;

type BaselineState = {
  check: CheckFn;
  opts?: NotifyBaselineOptions;
  /** The snapshot already handled; starts as the data cached at mount. */
  data?: NotifySnapshot;
  baseline: Map<string, string> | null;
};

function snapshotOf(state: BaselineState, sessions: NotifyEntry[]) {
  const key = state.opts?.key ?? notifyStateKey;
  return new Map(sessions.map((s) => [s.id, key(s)]));
}

// Run check and honour its "re-snapshot" request.
function runCheck(state: BaselineState, sessions: NotifyEntry[]) {
  if (state.check(sessions, state.baseline)) state.baseline = snapshotOf(state, sessions);
}

/**
 * useNotifyBaseline owns the boilerplate shared by the favicon, bell,
 * OS-notification and in-app-toast hooks:
 *
 *   - consuming the shared notify query,
 *   - invoking `check(sessions)` for every new snapshot,
 *   - maintaining a baseline snapshot (id -> key) that resets when
 *     the tab becomes visible and is re-taken (then checked once) when
 *     it goes hidden,
 *   - taking an initial baseline from data that exists at mount.
 *
 * The caller supplies `check`, which receives the current baseline (or
 * null when no snapshot has been taken) so it can skip sessions whose
 * state matched at snapshot time. Returning `true` asks the hook to
 * re-snapshot the baseline from the just-checked sessions (the bell uses
 * this after ringing so it doesn't ring again for the same events).
 */
export function useNotifyBaseline(check: CheckFn, opts?: NotifyBaselineOptions): void {
  const data = useNotifySnapshot();
  const stateRef = useRef<BaselineState>({ check, opts, data, baseline: null });
  // Keep the latest callbacks current for the listeners created on mount.
  useEffect(() => {
    stateRef.current.check = check;
    stateRef.current.opts = opts;
  });

  useEffect(() => {
    const state = stateRef.current;
    if (!data || data === state.data) return;
    state.data = data;
    runCheck(state, data.entries);
  });

  useEffect(() => {
    const state = stateRef.current;
    function onVisibilityChange() {
      if (state.opts?.ignoreVisibility) return;
      const sessions = state.data?.entries;
      if (!document.hidden) {
        state.baseline = null;
        state.opts?.onVisibleReset?.();
        if (sessions && state.opts?.checkWhenShown) runCheck(state, sessions);
      } else if (sessions) {
        state.baseline = snapshotOf(state, sessions);
        runCheck(state, sessions);
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange);

    const initial = state.data?.entries;
    const initialBaseline = state.opts?.initialBaseline ?? 'hidden';
    if (initial && (initialBaseline === 'always' || (initialBaseline === 'hidden' && document.hidden))) {
      state.baseline = snapshotOf(state, initial);
    }
    if (initial && state.opts?.checkWhenShown) runCheck(state, initial);

    return () => document.removeEventListener('visibilitychange', onVisibilityChange);
  }, []);
}
