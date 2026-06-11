import { useEffect } from 'react';
import { notifyPromptDismissed } from './useToastNotify';
import { recheckNotifyData } from './useNotifyData';

/**
 * App-wide SSE subscriber for `/api/events`.
 *
 * Unlike the per-session SSE stream in `useSession` (which is only
 * connected for the session the user is currently viewing), this stream
 * carries cross-page broadcast events that every connected client
 * should react to regardless of which page they're on.
 *
 * Events handled:
 *   - `ocman.permission.resolved` / `ocman.question.resolved`: a prompt
 *     is no longer pending (auto-approved by the judge, or answered via
 *     the OpenCode TUI / another tab). Drops the matching prompt toast
 *     for that *background* session immediately instead of waiting for
 *     the next 10s `/api/sessions/notify` poll.
 *   - `ocman.permission.flagged`: the judge flagged a permission for
 *     human review. A new prompt now needs attention — force a notify
 *     recheck so the bell / favicon / toast surface it promptly.
 *   - `ocman.session.idle`: a session finished a turn. Force a recheck
 *     so the completed-but-unseen indicators update promptly.
 *
 * A single shared EventSource is reference-counted across all hook
 * consumers so we never open more than one connection per tab.
 */

let source: EventSource | null = null;
let refCount = 0;

/** Payload shape carrying a session id (all broadcast events have one). */
type SessionEventPayload = {
  sessionID?: string;
  permissionId?: string;
  requestId?: string;
  reason?: string;
};

function parsePayload(raw: string): SessionEventPayload | null {
  try {
    return JSON.parse(raw) as SessionEventPayload;
  } catch {
    return null;
  }
}

/**
 * Handle a prompt-resolved broadcast (permission or question): drop any
 * toast for the named session and force a notify recheck as a backstop
 * so the bell / favicon / OS-notification baselines also catch up.
 */
function handleResolved(raw: string): void {
  const parsed = parsePayload(raw);
  const sessionId = parsed?.sessionID;
  if (!sessionId) return;
  notifyPromptDismissed(sessionId);
  recheckNotifyData();
}

/**
 * Handle a broadcast that should surface a session sooner (a permission
 * got flagged, or a session went idle): force a notify recheck so the
 * notification surfaces ahead of the next poll. No toast is dismissed.
 */
function handleSurface(raw: string): void {
  const parsed = parsePayload(raw);
  if (!parsed?.sessionID) return;
  recheckNotifyData();
}

function open(): void {
  if (source) return;
  source = new EventSource('/api/events');
  source.addEventListener('ocman.permission.resolved', (e) => {
    handleResolved((e as MessageEvent).data);
  });
  source.addEventListener('ocman.question.resolved', (e) => {
    handleResolved((e as MessageEvent).data);
  });
  source.addEventListener('ocman.permission.flagged', (e) => {
    handleSurface((e as MessageEvent).data);
  });
  source.addEventListener('ocman.session.idle', (e) => {
    handleSurface((e as MessageEvent).data);
  });
  // EventSource auto-reconnects on transient errors; nothing to do here
  // beyond letting it retry. A hard failure (non-200) stops it, in which
  // case the notify poll remains the safety net.
}

function close(): void {
  source?.close();
  source = null;
}

/**
 * Subscribe to the shared `/api/events` stream for the lifetime of the
 * mounted component. Mount once at the app root (alongside the other
 * notify hooks). Idempotent across multiple consumers.
 */
export function useGlobalEvents(): void {
  useEffect(() => {
    refCount += 1;
    if (refCount === 1) open();
    return () => {
      refCount = Math.max(0, refCount - 1);
      if (refCount === 0) close();
    };
  }, []);
}

/** Test-only: tear down the shared connection and reset refcount. */
export function __resetForTests(): void {
  close();
  refCount = 0;
}

/** Test-only: dispatch a raw resolved payload through the handler. */
export function __handleResolvedForTests(raw: string): void {
  handleResolved(raw);
}

/** Test-only: dispatch a raw surface (flagged/idle) payload. */
export function __handleSurfaceForTests(raw: string): void {
  handleSurface(raw);
}
