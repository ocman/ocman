import { useEffect, useRef } from 'react';
import type { NotifyEntry } from './api';
import { notifyStateKey, useNotifyBaseline } from './useNotifyBaseline';
import { useUiStore } from './uiStore';
import { pushSubscribed, showSystemNotification } from './pushNotifications';

/**
 * Fires OS-level Web Notifications (system toasts / installed-PWA
 * notifications) when sessions complete or block on user input.
 *
 * Sibling to useFaviconNotify and useBellNotify — same data source,
 * same baseline-snapshot pattern. Each hook is independent so users can
 * enable any subset of {favicon, bell, notification} without one
 * affecting the others.
 *
 * Two trigger paths:
 *
 *   - **completed**: an unseen session transitioned to `waiting` or `error`.
 *
 *   - **prompt**: a session is blocked on a permission request or
 *     question. Marked `requireInteraction: true` so the toast sticks
 *     until dismissed.
 *
 * Both paths fire only while the app is hidden or unfocused. In-app
 * indicators cover the visible, focused case without a system banner.
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
  platform?: string;
  title?: string;
  directory?: string;
  question?: boolean;
  failed?: boolean;
  permission?: boolean;
};

type EvaluateInput = {
  sessions: NotifyShape[];
  hidden: boolean;
  focused: boolean;
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
  if (!input.hidden && input.focused) return [];

  const out: Decision[] = [];
  for (const s of input.sessions) {
    const isPrompt = s.pendingPermission === true || s.pendingQuestion === true;
    const isCompleted = (s.status === 'waiting' || s.status === 'error') && !s.seen;

    // Prompt wins over completed for the same session — only one
    // notification per session per tick. Mirrors the favicon hook's
    // priority order.
    let kind: 'prompt' | 'completed' | null = null;
    if (isPrompt) kind = 'prompt';
    else if (isCompleted) kind = 'completed';
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
    const dedupeKey = `${s.platform ?? ''}:${s.id}|${key}`;
    if (firedKeys.has(dedupeKey)) continue;
    firedKeys.add(dedupeKey);

    out.push({ kind, sessionId: s.id, ...(s.platform ? { platform: s.platform } : {}),
      ...(s.title ? { title: s.title } : {}), ...(s.directory ? { directory: s.directory } : {}),
      ...(s.status === 'error' ? { failed: true } : {}),
      ...(s.pendingPermission ? { permission: true } : s.pendingQuestion ? { question: true } : {}) });
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

export function __notificationDetailsForTests(d: Decision) {
  const isPrompt = d.kind === 'prompt';
  const title = Array.from(d.title?.replace(/\s+/g, ' ').trim() || 'Untitled session').slice(0, 160).join('');
  const action = isPrompt ? d.permission ? 'Permission needed' : d.question ? 'Answer needed' : 'Input needed' : d.failed ? 'Failed' : 'Finished';
  const project = d.directory?.replaceAll('\\', '/').split('/').filter(Boolean).at(-1);
  const body = project ? `${action} · ${Array.from(project).slice(0, 80).join('')}` : action;
  const params = new URLSearchParams({ notification: '1', ...(d.platform ? { platform: d.platform } : {}) });
  const url = `/session/${encodeURIComponent(d.sessionId)}?${params}`;
  return {
    title,
    body,
    url,
    tag: `ocman:${d.platform ? `${d.platform}:` : ''}${d.sessionId}:${d.kind}`,
    requireInteraction: isPrompt,
  };
}

async function spawnNotification(d: Decision) {
  if (typeof Notification === 'undefined') return;
  const details = __notificationDetailsForTests(d);
  try {
    // The backend owns these alerts on subscribed devices; polling must not
    // create a second toast for the same event.
    if (await pushSubscribed()) return;
    await showSystemNotification(details.title, {
      body: details.body,
      icon: '/apple-touch-icon.png',
      badge: '/favicon-32.png',
      tag: details.tag,
      requireInteraction: details.requireInteraction,
      // Stash the target on the notification so the click handler can
      // route to the right page. `data` survives across the SW
      // boundary too (used by the SW's notificationclick handler).
      data: { ...d, url: details.url },
    });
  } catch {
    // Browser revocation or OS restrictions can still reject display.
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
        focused: document.hasFocus(),
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
      initialBaseline: 'always',
      // Reset the per-tab dedupe set when the tab becomes visible so the
      // next hide-then-complete cycle starts fresh.
      onVisibleReset: () => {
        firedKeys = new Set();
      },
    },
  );
}
