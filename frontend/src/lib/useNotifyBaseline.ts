import { useEffect, useRef } from 'react';
import type { NotifyEntry } from './api';
import { useNotifyStore } from './useNotifyData';

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

/**
 * useNotifyBaseline owns the boilerplate shared by the favicon, bell,
 * OS-notification and in-app-toast hooks:
 *
 *   - subscribing to / unsubscribing from the shared useNotifyStore,
 *   - reacting to new store data by invoking `check(sessions)`,
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
export function useNotifyBaseline(
  check: (sessions: NotifyEntry[], baseline: Map<string, string> | null) => boolean | void,
  opts?: NotifyBaselineOptions,
): void {
  const baselineRef = useRef<Map<string, string> | null>(null);
  // Keep the latest callbacks in refs so the store subscription closure
  // (created once on mount) always sees current values.
  const checkRef = useRef(check);
  const optsRef = useRef(opts);
  useEffect(() => {
    checkRef.current = check;
    optsRef.current = opts;
  });

  useEffect(() => {
    useNotifyStore.getState().subscribe();

    const snapshot = (sessions: NotifyEntry[]) => {
      const key = optsRef.current?.key ?? notifyStateKey;
      return new Map(sessions.map((s) => [s.id, key(s)]));
    };

    // Run check and honour its "re-snapshot" request.
    const runCheck = (sessions: NotifyEntry[]) => {
      if (checkRef.current(sessions, baselineRef.current)) {
        baselineRef.current = snapshot(sessions);
      }
    };

    function onVisibilityChange() {
      if (optsRef.current?.ignoreVisibility) return;
      const sessions = useNotifyStore.getState().data;
      if (!document.hidden) {
        baselineRef.current = null;
        optsRef.current?.onVisibleReset?.();
        if (sessions && optsRef.current?.checkWhenShown) runCheck(sessions);
      } else if (sessions) {
        baselineRef.current = snapshot(sessions);
        runCheck(sessions);
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange);

    const initial = useNotifyStore.getState().data;
    const initialBaseline = optsRef.current?.initialBaseline ?? 'hidden';
    if (initial && (initialBaseline === 'always' || (initialBaseline === 'hidden' && document.hidden))) {
      baselineRef.current = snapshot(initial);
    }

    const unsub = useNotifyStore.subscribe((state) => {
      if (state.data) runCheck(state.data);
    });

    if (initial && optsRef.current?.checkWhenShown) runCheck(initial);

    return () => {
      unsub();
      document.removeEventListener('visibilitychange', onVisibilityChange);
      useNotifyStore.getState().unsubscribe();
    };
  }, []);
}
