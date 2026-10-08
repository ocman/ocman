import { useEffect } from 'react';
import { notifyPromptDismissed } from './useToastNotify';
import { recheckNotifyData } from './useNotifyData';
import { markBackendReachable, markBackendUnreachable } from './backendStatus';
import { onPageResume } from './pageResume';
import type { QueuedMessage, Session } from './api';
import { clearSettingsCache } from './projectSettingsCache';

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
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectAttempt = 0;
let unsubscribeResume: (() => void) | null = null;

const reconnectBaseMs = 1_000;
const reconnectMaxMs = 60_000;

function nextReconnectDelay(): number {
  const target = Math.min(reconnectMaxMs, reconnectBaseMs * 2 ** reconnectAttempt);
  reconnectAttempt += 1;
  return target / 2 + Math.random() * target / 2;
}

/** Payload shape carrying a session id (all broadcast events have one). */
type SessionEventPayload = {
  sessionID?: string;
  platform?: string;
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
  /** Fields that can be applied directly to an existing session row. */
  patch?: Partial<Session>;
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
  const requestId = parsed?.permissionId ?? parsed?.requestId;
  recheckNotifyData(requestId ? {
    platform: parsed?.platform ?? 'opencode', sessionId, requestId,
    kind: parsed?.permissionId ? 'permission' : 'question',
  } : undefined);
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

// sessionChangedListeners: subscribers (e.g. the App-level query client)
// react to a session.changed broadcast by refreshing the session list,
// so a newly-created session appears immediately instead of on the next
// poll tick.
const sessionChangedListeners = new Set<(
  sessionId: string,
  session?: Session,
  patch?: Partial<Session>,
  platform?: string,
) => void>();

const projectsChangedListeners = new Set<() => void>();
export type GitCommandHint = {
  sessionID: string;
  action: 'push' | 'commit';
  remoteId: string;
  projectId: string;
  directory: string;
};
const gitCommandListeners = new Set<(hint: GitCommandHint) => void>();

export function onGitCommand(cb: (hint: GitCommandHint) => void): () => void {
  gitCommandListeners.add(cb);
  return () => gitCommandListeners.delete(cb);
}

function handleGitCommand(raw: string): void {
  try {
    const hint = JSON.parse(raw);
    if (!hint || typeof hint.sessionID !== 'string' || !hint.sessionID ||
        (hint.action !== 'push' && hint.action !== 'commit') ||
        typeof hint.remoteId !== 'string' || !hint.remoteId ||
        typeof hint.projectId !== 'string' || typeof hint.directory !== 'string') return;
    for (const cb of gitCommandListeners) cb(hint);
  } catch { /* Ignore malformed SSE payloads. */ }
}
const sessionActivityListeners = new Set<(sessionId: string, timeUpdated: number) => void>();

export function onSessionActivity(cb: (sessionId: string, timeUpdated: number) => void): () => void {
  sessionActivityListeners.add(cb);
  return () => sessionActivityListeners.delete(cb);
}

function handleSessionActivity(raw: string): void {
  try {
    const { sessionID, timeUpdated } = JSON.parse(raw);
    if (typeof sessionID !== 'string' || !sessionID ||
        typeof timeUpdated !== 'number' || !Number.isFinite(timeUpdated) || timeUpdated <= 0) return;
    for (const cb of sessionActivityListeners) cb(sessionID, timeUpdated);
  } catch { /* Ignore malformed SSE payloads. */ }
}
const inboxChangedListeners = new Set<() => void>();

export type StartStep = 'opencode' | 'worktree' | 'session' | 'prompt';
export type StartStepState = 'active' | 'done' | 'error';
const startProgressListeners = new Set<(startId: string, step: StartStep, state: StartStepState) => void>();

/** Register a callback fired on every ocman.session.start.progress broadcast. */
export function onSessionStartProgress(cb: (startId: string, step: StartStep, state: StartStepState) => void): () => void {
  startProgressListeners.add(cb);
  return () => startProgressListeners.delete(cb);
}

function handleStartProgress(raw: string): void {
  try {
    const { startId, step, state } = JSON.parse(raw);
    if (typeof startId !== 'string' || typeof step !== 'string' || typeof state !== 'string') return;
    for (const cb of startProgressListeners) cb(startId, step as StartStep, state as StartStepState);
  } catch { /* Ignore malformed SSE payloads. */ }
}
const artifactCreatedListeners = new Set<() => void>();

/** Register a callback fired on every ocman.artifact.created broadcast. */
export function onArtifactCreated(cb: () => void): () => void {
  artifactCreatedListeners.add(cb);
  return () => artifactCreatedListeners.delete(cb);
}

export function onInboxChanged(cb: () => void): () => void {
  inboxChangedListeners.add(cb);
  return () => inboxChangedListeners.delete(cb);
}

export function onProjectsChanged(cb: () => void): () => void {
  projectsChangedListeners.add(cb);
  return () => projectsChangedListeners.delete(cb);
}

function handleProjectsChanged(): void {
  for (const cb of projectsChangedListeners) cb();
}

/**
 * Register a callback fired on every ocman.session.changed broadcast.
 * The second arg is a provisional session row when the event carries
 * one (freshly-created sessions), so listeners can insert it before the
 * authoritative refetch.
 * The fourth arg is the owning compound platform when supplied.
 */
export function onSessionChanged(
  cb: (sessionId: string, session?: Session, patch?: Partial<Session>, platform?: string) => void,
): () => void {
  sessionChangedListeners.add(cb);
  return () => sessionChangedListeners.delete(cb);
}

function handleSessionChanged(raw: string): void {
  const parsed = parsePayload(raw);
  const sessionId = parsed?.sessionID;
  if (!sessionId) return;
  for (const cb of sessionChangedListeners) cb(sessionId, parsed?.session, parsed?.patch, parsed?.platform ?? parsed?.session?.platform);
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
  const next = new EventSource('/api/events');
  source = next;
  next.addEventListener('ocman.settings.changed', clearSettingsCache);
  next.addEventListener('ocman.inbox.changed', () => {
    for (const cb of inboxChangedListeners) cb();
  });
  next.onopen = () => {
    clearSettingsCache();
    reconnectAttempt = 0;
    markBackendReachable();
    // Reconcile consumers after the first open and every replacement stream.
    for (const cb of connectListeners) cb();
  };
  next.addEventListener('ocman.permission.resolved', (e) => {
    handleResolved((e as MessageEvent).data);
  });
  next.addEventListener('ocman.question.resolved', (e) => {
    handleResolved((e as MessageEvent).data);
  });
  next.addEventListener('ocman.permission.flagged', (e) => {
    handleSurface((e as MessageEvent).data);
  });
  next.addEventListener('ocman.session.idle', (e) => {
    handleSurface((e as MessageEvent).data);
  });
  next.addEventListener('ocman.session.changed', (e) => {
    handleSessionChanged((e as MessageEvent).data);
  });
  next.addEventListener('ocman.session.activity', (e) => {
    handleSessionActivity((e as MessageEvent).data);
  });
  next.addEventListener('ocman.projects.changed', handleProjectsChanged);
  next.addEventListener('ocman.git.command', (e) => {
    handleGitCommand((e as MessageEvent).data);
  });
  next.addEventListener('ocman.artifact.created', () => {
    for (const cb of artifactCreatedListeners) cb();
  });
  next.addEventListener('ocman.session.start.progress', (e) => {
    handleStartProgress((e as MessageEvent).data);
  });
  next.addEventListener('ocman.queue.updated', (e) => {
    handleQueueUpdated((e as MessageEvent).data);
  });
  next.onerror = () => {
    if (source !== next) return;
    next.close();
    source = null;
    const delay = nextReconnectDelay();
    if (delay >= 5_000) markBackendUnreachable('Live event stream disconnected.');
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      if (refCount > 0) open();
    }, delay);
  };
}

/**
 * Replace the stream after the user returns: it may be half-open with
 * missed events, or waiting out a long backoff. The new stream's onopen
 * runs the connect listeners, which reconcile the gap.
 */
function recycle(): void {
  close();
  open();
}

function close(): void {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  source?.close();
  source = null;
  reconnectAttempt = 0;
}

/**
 * Subscribe to the shared `/api/events` stream for the lifetime of the
 * mounted component. Mount once at the app root (alongside the other
 * notify hooks). Idempotent across multiple consumers.
 */
export function useGlobalEvents(): void {
  useEffect(() => {
    refCount += 1;
    if (refCount === 1) {
      open();
      unsubscribeResume = onPageResume(recycle);
    }
    return () => {
      refCount = Math.max(0, refCount - 1);
      if (refCount === 0) stop();
    };
  }, []);
}

function stop(): void {
  unsubscribeResume?.();
  unsubscribeResume = null;
  close();
}

/** Test-only: tear down the shared connection and reset refcount. */
export function __resetForTests(): void {
  stop();
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

export function __handleProjectsChangedForTests(): void {
  handleProjectsChanged();
}

/** Test-only: dispatch a raw queue.updated payload. */
export function __handleQueueUpdatedForTests(raw: string): void {
  handleQueueUpdated(raw);
}
