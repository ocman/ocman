import { useEffect, useRef } from 'react';
import type { NotifyEntry } from './api';
import { notifyStateKey, useNotifyBaseline } from './useNotifyBaseline';
import { useUiStore } from './uiStore';

/**
 * Fires OS-level Web Notifications (system toasts / installed-PWA
 * notifications) when sessions complete or block on user input.
 *
 * Sibling to useFaviconNotify and useBellNotify — same data source,
 * same baseline-snapshot pattern. Each hook is independent so users can
 * enable any subset of {favicon, bell, notification} without one
 * affecting the others.
 *
 * Now consumes the shared `useNotifyStore` instead of polling
 * `/api/sessions/notify` independently (P2 fix).
 *
 * Two trigger paths:
 *
 *   - **completed**: a session transitioned to `waiting` or `error` after
 *     the tab went hidden. Fires only when `document.hidden` is true so
 *     we don't pop a toast over the page the user is actively looking
 *     at — the favicon already covers the visible-tab case.
 *
 *   - **prompt**: a session is blocked on a permission request or
 *     question. Fires regardless of visibility because the session
 *     literally cannot proceed without the user's input. Marked
 *     `requireInteraction: true` so the toast sticks until dismissed.
 *
 * Click handling is split between this hook (focuses the current tab
 * and navigates) and the service worker's `notificationclick` listener
 * (handles clicks when no page is open, e.g. the installed PWA was
 * launched fresh from the dock).
 */

/** Minimal notify-entry shape consumed by the controller. Exported for tests. */
export type NotifyShape = NotifyEntry;

export type Decision = {
  kind: 'completed' | 'prompt';
  sessionId: string;
};

type EvaluateInput = {
  sessions: NotifyShape[];
  hidden: boolean;
  permission: NotificationPermission | 'unsupported';
  enabled: boolean;
  /** Map of sessionId -> stateKey at the time the baseline was taken. */
  baseline: Map<string, string> | null;
};

// State key used to dedupe notifications across polls.
const stateKey = notifyStateKey;

// Sessions that have *already triggered* a notification this tab session.
// Cleared on visibility change (mirrors how the bell/favicon baselines
// reset). Tracked at module scope so the hook can be re-rendered without
// losing dedupe state, and so tests can flush it via __resetForTests.
let firedKeys = new Set<string>();

/**
 * Pure decision function. Given a snapshot of session state plus
 * environment context, returns the list of notifications that should
 * fire right now. The caller is responsible for actually constructing
 * `Notification` objects from the result.
 *
 * Exported under a `__` prefix so it's clearly a test seam, not part of
 * the public API.
 */
export function __evaluateForTests(input: EvaluateInput): Decision[] {
  if (!input.enabled) return [];
  if (input.permission !== 'granted') return [];

  const out: Decision[] = [];
  for (const s of input.sessions) {
    const isPrompt = s.pendingPermission === true || s.pendingQuestion === true;
    const isCompleted = (s.status === 'waiting' || s.status === 'error') && !s.seen;

    // Prompt wins over completed for the same session — only one
    // notification per session per tick. Mirrors the favicon hook's
    // priority order.
    let kind: 'prompt' | 'completed' | null = null;
    if (isPrompt) kind = 'prompt';
    else if (isCompleted && input.hidden) kind = 'completed';
    if (kind === null) continue;

    // Baseline filter: skip sessions whose state matches what we saw
    // when the baseline was captured. A null baseline means "no
    // snapshot taken yet" (we're not currently in a hidden cycle), in
    // which case we treat everything as new — mirrors useBellNotify.
    const key = stateKey(s);
    if (input.baseline !== null && input.baseline.get(s.id) === key) continue;

    // Per-session dedupe across polls: don't fire for the same
    // sessionId+key twice in a row, even if the polling cycle picks it
    // up multiple times before the baseline is rebuilt.
    const dedupeKey = `${s.id}|${key}`;
    if (firedKeys.has(dedupeKey)) continue;
    firedKeys.add(dedupeKey);

    out.push({ kind, sessionId: s.id });
  }
  return out;
}

/** Test-only: flush the per-tab "already fired" cache. */
export function __resetForTests() {
  firedKeys = new Set();
}

/** Returns the current Notification permission, or 'unsupported' on platforms without the API. */
function readPermission(): NotificationPermission | 'unsupported' {
  if (typeof window === 'undefined') return 'unsupported';
  if (typeof Notification === 'undefined') return 'unsupported';
  return Notification.permission;
}

/**
 * Request permission to display notifications. Resolves with the
 * resulting permission value. Safe to call when the API is unavailable
 * — returns `'denied'` so callers don't have to special-case missing
 * support.
 */
export async function requestNotificationPermission(): Promise<NotificationPermission> {
  if (typeof Notification === 'undefined') return 'denied';
  try {
    return await Notification.requestPermission();
  } catch {
    return 'denied';
  }
}

/** True when the runtime exposes the Notification API at all. */
export function notificationsSupported(): boolean {
  return typeof window !== 'undefined' && typeof Notification !== 'undefined';
}

function spawnNotification(d: Decision) {
  if (typeof Notification === 'undefined') return;
  const isPrompt = d.kind === 'prompt';
  const title = isPrompt ? 'ocman — input required' : 'ocman — session finished';
  const body = isPrompt
    ? 'A session is waiting on your response.'
    : 'A coding-agent session has finished running.';
  try {
    const n = new Notification(title, {
      body,
      icon: '/apple-touch-icon.png',
      badge: '/favicon-32.png',
      tag: `ocman:${d.sessionId}:${d.kind}`,
      requireInteraction: isPrompt,
      // Stash the session id on the notification so the click handler
      // can route to the right page. `data` survives across the SW
      // boundary too (used by the SW's notificationclick handler).
      data: { sessionId: d.sessionId, kind: d.kind, url: `/session/${d.sessionId}` },
    });
    n.onclick = (event) => {
      event.preventDefault();
      window.focus();
      // Use replace-style nav so the back button returns to wherever
      // the user came from rather than a blank tab.
      window.location.assign(`/session/${d.sessionId}`);
      n.close();
    };
  } catch {
    // Some platforms throw on `new Notification()` directly inside an
    // installed PWA and require ServiceWorkerRegistration.showNotification
    // instead. We swallow rather than surfacing — the favicon/title
    // and bell already cover the no-toast case.
  }
}

export function useNotificationNotify() {
  const enabled = useUiStore((s) => s.notificationsEnabled);
  const enabledRef = useRef(enabled);
  useEffect(() => {
    enabledRef.current = enabled;
  }, [enabled]);

  useNotifyBaseline(
    (sessions, baseline) => {
      if (!enabledRef.current) return;
      const permission = readPermission();
      if (permission !== 'granted') return;

      const decisions = __evaluateForTests({
        sessions,
        hidden: document.hidden,
        permission,
        enabled: enabledRef.current,
        baseline,
      });
      for (const d of decisions) spawnNotification(d);
    },
    {
      // Snapshot at mount even when visible, so a prompt that arrives
      // while the user is on another app still triggers — but a prompt
      // that was *already* there before mount doesn't.
      initialBaselineWhenVisible: true,
      // Reset the per-tab dedupe set when the tab becomes visible so the
      // next hide-then-complete cycle starts fresh.
      onVisibleReset: () => {
        firedKeys = new Set();
      },
    },
  );
}
