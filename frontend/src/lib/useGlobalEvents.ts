import { useEffect } from 'react';
import { notifyPromptDismissed } from './useToastNotify';
import { recheckNotifyData } from './useNotifyData';
import type { QueuedMessage, Session } from './api';

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
  messages?: QueuedMessage[];
  /**
   * Provisional session row carried by an ocman.session.changed
   * broadcast for a freshly-created session. Lets listeners insert the
   * row optimistically before the authoritative refetch lands. Absent
   * for change events that only know the id.
   */
  session?: Session;
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

// loopUpdated listeners: the loops store registers here so an agent-loop
// state change broadcast (loop.updated) triggers a live refresh without
// opening a second EventSource (AD-10).
const loopUpdatedListeners = new Set<(loopId: string) => void>();

/** Register a callback fired on every loop.updated broadcast. */
export function onLoopUpdated(cb: (loopId: string) => void): () => void {
  loopUpdatedListeners.add(cb);
  return () => loopUpdatedListeners.delete(cb);
}

function handleLoopUpdated(raw: string): void {
  let loopId = '';
  try {
    loopId = (JSON.parse(raw) as { loopId?: string }).loopId ?? '';
  } catch {
    return;
  }
  for (const cb of loopUpdatedListeners) cb(loopId);
}

// sessionChangedListeners: subscribers (e.g. the App-level query client)
// react to a session.changed broadcast by refreshing the session list,
// so a newly-created session appears immediately instead of on the next
// poll tick.
const sessionChangedListeners = new Set<(sessionId: string, session?: Session) => void>();

/**
 * Register a callback fired on every ocman.session.changed broadcast.
 * The second arg is a provisional session row when the event carries
 * one (freshly-created sessions), so listeners can insert it before the
 * authoritative refetch.
 */
export function onSessionChanged(cb: (sessionId: string, session?: Session) => void): () => void {
  sessionChangedListeners.add(cb);
  return () => sessionChangedListeners.delete(cb);
}

function handleSessionChanged(raw: string): void {
  const parsed = parsePayload(raw);
  const sessionId = parsed?.sessionID;
  if (!sessionId) return;
  for (const cb of sessionChangedListeners) cb(sessionId, parsed?.session);
}

// queueUpdatedListeners: the composer's queue view registers here so a
// follow-up-queue change broadcast (ocman.queue.updated) updates the list
// live — from any client, not just the one that mutated it (#58). The
// event carries the session's full queue, so listeners apply it directly
// without a refetch; messages is undefined only when the payload omitted
// it (older server / marshal miss), in which case listeners refetch.
const queueUpdatedListeners = new Set<(sessionId: string, messages?: QueuedMessage[]) => void>();

/** Register a callback fired on every ocman.queue.updated broadcast. */
export function onQueueUpdated(cb: (sessionId: string, messages?: QueuedMessage[]) => void): () => void {
  queueUpdatedListeners.add(cb);
  return () => queueUpdatedListeners.delete(cb);
}

function handleQueueUpdated(raw: string): void {
  const parsed = parsePayload(raw);
  const sessionId = parsed?.sessionID;
  if (!sessionId) return;
  for (const cb of queueUpdatedListeners) cb(sessionId, parsed?.messages);
}

// connectListeners fire every time the shared /api/events stream (re)opens
// — the initial connect and every reconnect after a drop. Subscribers that
// mirror server state over this stream (e.g. the follow-up queue) reload
// from their endpoint on this signal to reconcile anything missed during
// the gap — the same "refetch on (re)connect, then live-update" pattern the
// conversation SSE uses.
const connectListeners = new Set<() => void>();

/** Register a callback fired whenever /api/events (re)connects. */
export function onSseConnect(cb: () => void): () => void {
  connectListeners.add(cb);
  return () => connectListeners.delete(cb);
}

function open(): void {
  if (source) return;
  source = new EventSource('/api/events');
  source.onopen = () => {
    // Fires on the first open and after every browser auto-reconnect.
    for (const cb of connectListeners) cb();
  };
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
  source.addEventListener('ocman.session.changed', (e) => {
    handleSessionChanged((e as MessageEvent).data);
  });
  source.addEventListener('loop.updated', (e) => {
    handleLoopUpdated((e as MessageEvent).data);
  });
  source.addEventListener('ocman.queue.updated', (e) => {
    handleQueueUpdated((e as MessageEvent).data);
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

/** Test-only: dispatch a raw session.changed payload. */
export function __handleSessionChangedForTests(raw: string): void {
  handleSessionChanged(raw);
}

/** Test-only: dispatch a raw queue.updated payload. */
export function __handleQueueUpdatedForTests(raw: string): void {
  handleQueueUpdated(raw);
}
