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

/**
 * useNotifyBaseline owns the boilerplate shared by useBellNotify and
 * useNotificationNotify:
 *
 *   - subscribing to / unsubscribing from the shared useNotifyStore,
 *   - reacting to new store data by invoking `check(sessions)`,
 *   - maintaining a baseline snapshot (id -> stateKey) that resets when
 *     the tab becomes visible and is re-taken (then checked once) when
 *     it goes hidden,
 *   - taking an initial baseline when the tab is already hidden at mount.
 *
 * The caller supplies `check`, which receives the current baseline (or
 * null when no snapshot has been taken) so it can skip sessions whose
 * state matched at snapshot time. Returning `true` asks the hook to
 * re-snapshot the baseline from the just-checked sessions (the bell uses
 * this after ringing so it doesn't ring again for the same events).
 * `onVisibleReset` lets a caller clear extra state (e.g. the
 * OS-notification dedupe set) when the tab becomes visible.
 */
export function useNotifyBaseline(
  check: (sessions: NotifyEntry[], baseline: Map<string, string> | null) => boolean | void,
  opts?: { initialBaselineWhenVisible?: boolean; onVisibleReset?: () => void },
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

    const snapshot = (sessions: NotifyEntry[]) =>
      new Map(sessions.map((s) => [s.id, notifyStateKey(s)]));

    // Run check and honour its "re-snapshot" request.
    const runCheck = (sessions: NotifyEntry[]) => {
      if (checkRef.current(sessions, baselineRef.current)) {
        baselineRef.current = snapshot(sessions);
      }
    };

    function onVisibilityChange() {
      const sessions = useNotifyStore.getState().data;
      if (!document.hidden) {
        baselineRef.current = null;
        optsRef.current?.onVisibleReset?.();
      } else if (sessions) {
        baselineRef.current = snapshot(sessions);
        runCheck(sessions);
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange);

    // Initial baseline. Bell only snapshots when already hidden;
    // useNotificationNotify also snapshots when visible so a prompt that
    // arrives while the user is away still triggers without re-firing for
    // prompts that predate mount.
    const initial = useNotifyStore.getState().data;
    if (initial && (document.hidden || optsRef.current?.initialBaselineWhenVisible)) {
      baselineRef.current = snapshot(initial);
    }

    const unsub = useNotifyStore.subscribe((state) => {
      if (state.data) runCheck(state.data);
    });

    return () => {
      unsub();
      document.removeEventListener('visibilitychange', onVisibilityChange);
      useNotifyStore.getState().unsubscribe();
    };
  }, []);
}
