import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useLocation } from 'react-router-dom';
import { api, type NotifyEntry } from './api';

/**
 * In-app Radix toast notifier for sessions that need user input
 * (pending permission request or question).
 *
 * Sibling to useFaviconNotify / useBellNotify / useNotificationNotify
 * — same `/api/sessions/notify` source, same baseline-snapshot pattern,
 * same 10s cadence. Each hook is independent.
 *
 * Why a fourth hook rather than reusing the OS notification path:
 *
 *   - OS notifications require an explicit permission grant and may
 *     never appear if the user dismissed the prompt. The in-app toast
 *     is always available.
 *   - When the tab is *focused but not on the session detail page*,
 *     the OS would skip the toast (permission gate; some platforms
 *     also suppress notifications for the focused window). Yet the
 *     user still might not know a session is asking for input. The
 *     in-app toast covers that gap.
 *
 * Trigger condition: a session has `pendingPermission` or
 * `pendingQuestion` set. Completed-but-not-seen sessions already get a
 * favicon dot, bell, and OS notification — toasting them too would be
 * noisy.
 *
 * Suppression: if the user is *currently viewing* a session, that
 * session's prompts are skipped. The detail page surfaces the prompt
 * inline so a toast for the same session would just be a duplicate.
 */

export type ToastNotifyShape = NotifyEntry;

export type ToastDecision = {
  sessionId: string;
  kind: 'permission' | 'question';
  title: string;
  directory: string;
};

const POLL_INTERVAL_MS = 10_000;
const TOAST_LOOKBACK_MS = 7 * 24 * 60 * 60 * 1000;
const TOAST_LIMIT = 500;

/** State key used to dedupe across polls. Mirrors useNotificationNotify. */
function stateKey(s: ToastNotifyShape): string {
  return `${s.status}|${s.pendingPermission ? '1' : '0'}|${s.pendingQuestion ? '1' : '0'}`;
}

// Sessions that have *already triggered* a toast for their current
// state key. Tracked at module scope so re-renders don't drop dedupe
// state, and so tests can flush via __resetForTests.
let firedKeys = new Set<string>();

// Module-scoped pub/sub for "this session's prompt was just resolved".
// Lets the SessionDetail page (which knows about resolution via SSE
// the moment it happens) tell the toast hook to drop any matching
// toast immediately, without waiting for the next 10s poll.
type DismissListener = (sessionId: string) => void;
const dismissListeners = new Set<DismissListener>();

/**
 * Notify the toast hook that the prompt for `sessionId` has been
 * resolved (answered, rejected, or otherwise no longer pending).
 * Removes any rendered toast for that session and clears its dedupe
 * entry so a fresh prompt later in the same session will re-toast.
 */
export function notifyPromptDismissed(sessionId: string): void {
  // Drop dedupe keys for this session so a future prompt can re-fire
  // even if the polled `notify` snapshot hasn't caught up yet.
  for (const k of firedKeys) {
    if (k.startsWith(`${sessionId}|`)) firedKeys.delete(k);
  }
  for (const listener of dismissListeners) listener(sessionId);
}

type EvaluateInput = {
  sessions: ToastNotifyShape[];
  /** Current router pathname; we suppress toasts for `/session/<id>`. */
  currentPath: string;
  /** Map of sessionId -> stateKey at baseline snapshot time. */
  baseline: Map<string, string> | null;
};

function activeSessionId(path: string): string | null {
  const m = path.match(/^\/session\/([^/?#]+)/);
  return m ? decodeURIComponent(m[1]) : null;
}

/**
 * Pure decision function. Given a snapshot of session state plus
 * environment context, returns the list of toasts that should appear
 * right now. The caller is responsible for actually rendering them.
 */
export function __evaluateForTests(input: EvaluateInput): ToastDecision[] {
  const skipId = activeSessionId(input.currentPath);
  const out: ToastDecision[] = [];

  // Build the set of dedupe keys that are still "live" for this tick.
  // Anything in firedKeys whose session is no longer in this state is
  // stale — drop it so the prompt can re-fire if it returns later
  // (e.g. user answers, agent asks again 30s later).
  const liveDedupe = new Set<string>();
  for (const s of input.sessions) {
    liveDedupe.add(`${s.id}|${stateKey(s)}`);
  }
  for (const k of firedKeys) {
    if (!liveDedupe.has(k)) firedKeys.delete(k);
  }

  for (const s of input.sessions) {
    if (s.id === skipId) continue;

    const isPermission = s.pendingPermission === true;
    const isQuestion = s.pendingQuestion === true;
    if (!isPermission && !isQuestion) continue;

    // Baseline filter: skip sessions whose state already matched at
    // baseline-snapshot time. Null baseline means "no snapshot taken
    // yet" — treat everything as new (mirrors sibling hooks).
    const key = stateKey(s);
    if (input.baseline !== null && input.baseline.get(s.id) === key) continue;

    // Per-session dedupe across polls.
    const dedupeKey = `${s.id}|${key}`;
    if (firedKeys.has(dedupeKey)) continue;
    firedKeys.add(dedupeKey);

    // Permission wins over question when both are flagged. Defensive:
    // shouldn't happen in practice but we want exactly one toast per
    // session per state transition.
    out.push({
      sessionId: s.id,
      kind: isPermission ? 'permission' : 'question',
      title: s.title ?? '',
      directory: s.directory ?? '',
    });
  }
  return out;
}

/** Test-only: flush the per-tab "already fired" cache. */
export function __resetForTests() {
  firedKeys = new Set();
  dismissListeners.clear();
}

/**
 * Live toast entry tracked by the React hook. Persistent (no auto
 * timeout) — the user dismisses it explicitly via the close button or
 * by clicking through to the session.
 */
export type ToastEntry = ToastDecision & {
  /** Stable id for the Radix Toast.Root key + open state. */
  toastId: string;
  /** Wall-clock ms when the toast was emitted; helps with ordering. */
  createdAt: number;
};

export type UseToastNotifyResult = {
  toasts: ToastEntry[];
  dismiss: (toastId: string) => void;
};

/**
 * Hook entry point. Returns the live list of pending-prompt toasts
 * plus a dismiss callback. Mount this once at app root and feed
 * `toasts` into a Radix `<Toast.Root>` map.
 */
export function useToastNotify(): UseToastNotifyResult {
  const location = useLocation();
  const pathRef = useRef(location.pathname);

  // Mirror the latest pathname into a ref so the polling closure can
  // read it without re-running the effect on every navigation. Done in
  // its own effect so we don't write to the ref during render.
  useEffect(() => {
    pathRef.current = location.pathname;
  }, [location.pathname]);

  const baselineRef = useRef<Map<string, string> | null>(null);
  const [toasts, setToasts] = useState<ToastEntry[]>([]);

  const dismiss = useCallback((toastId: string) => {
    setToasts((prev) => prev.filter((t) => t.toastId !== toastId));
  }, []);

  // Hide any toast whose session matches the page the user is
  // currently on. Computed at render time rather than mutated via an
  // effect — both because it avoids a cascading-render lint warning
  // and because it correctly handles the case where the user
  // navigates *away* from the session before the prompt resolves
  // (the toast should reappear in the viewport once they leave).
  const visibleToasts = useMemo(() => {
    const active = activeSessionId(location.pathname);
    if (!active) return toasts;
    return toasts.filter((t) => t.sessionId !== active);
  }, [toasts, location.pathname]);

  // Subscribe to module-scoped "prompt resolved" events fired by
  // SessionDetail when a permission/question reply is sent. Lets us
  // remove the toast instantly instead of waiting for the next poll.
  useEffect(() => {
    const listener: DismissListener = (sessionId) => {
      setToasts((prev) => prev.filter((t) => t.sessionId !== sessionId));
    };
    dismissListeners.add(listener);
    return () => {
      dismissListeners.delete(listener);
    };
  }, []);

  useEffect(() => {
    let cancelled = false;

    async function takeBaseline() {
      try {
        const sessions = await api.sessionsNotify({
          since: Date.now() - TOAST_LOOKBACK_MS,
          limit: TOAST_LIMIT,
        });
        if (cancelled) return;
        baselineRef.current = new Map(sessions.map((s) => [s.id, stateKey(s)]));
      } catch {
        baselineRef.current = null;
      }
    }

    async function check() {
      try {
        const sessions = await api.sessionsNotify({
          since: Date.now() - TOAST_LOOKBACK_MS,
          limit: TOAST_LIMIT,
        });
        if (cancelled) return;

        // Build the "still has a pending prompt of kind X" set so we
        // can prune resolved toasts. Defensive backstop for cross-tab
        // resolutions and any same-tab path that didn't fire
        // notifyPromptDismissed (e.g. session.idle event flow).
        const stillPending = new Set<string>();
        for (const s of sessions) {
          if (s.pendingPermission) stillPending.add(`${s.id}|permission`);
          if (s.pendingQuestion) stillPending.add(`${s.id}|question`);
        }

        const decisions = __evaluateForTests({
          sessions,
          currentPath: pathRef.current,
          baseline: baselineRef.current,
        });

        const now = Date.now();
        setToasts((prev) => {
          // First prune toasts whose underlying prompt resolved.
          const pruned = prev.filter((t) =>
            stillPending.has(`${t.sessionId}|${t.kind}`),
          );
          if (decisions.length === 0) return pruned;

          // Drop any pre-existing toast for the same session — the
          // newest decision supersedes it (e.g. question -> permission).
          const ids = new Set(decisions.map((d) => d.sessionId));
          const kept = pruned.filter((t) => !ids.has(t.sessionId));
          const next = decisions.map<ToastEntry>((d) => ({
            ...d,
            toastId: `${d.sessionId}:${now}`,
            createdAt: now,
          }));
          return [...kept, ...next];
        });
      } catch {
        // network errors shouldn't break anything — next poll retries
      }
    }

    // Snapshot baseline at mount so prompts that were *already* there
    // when the app loaded don't toast retroactively. Then poll on the
    // standard cadence.
    void takeBaseline();
    const id = setInterval(() => void check(), POLL_INTERVAL_MS);

    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  return { toasts: visibleToasts, dismiss };
}
