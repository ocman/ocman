// SSE event handlers for the SessionDetail reducer: messages, parts and status.
// Permission and question prompts are in sessionPromptReducer.ts.

import type { Message, Part } from './api';
import { backgroundSnapshotCount, reduceBackgroundTaskCount } from './sessionBackgroundTasks';
import type { SessionMetadata, SessionView, SseEvent } from './sessionViewTypes';
import { answeredQuestionRequestId, extractMessageFromEvent, isSessionStatusIdle } from './sseHelpers';
import { applyDelta, STREAMING_FIELDS, upsertMessage, upsertSnapshotPart } from './sessionViewParts';
import {
  reducePermissionAsked, reducePermissionAutoApproved, reducePermissionChecking, reducePermissionFlagged,
  reducePermissionPending, reducePermissionReplied, reduceQuestionAsked, reduceQuestionCleared,
} from './sessionPromptReducer';

/**
 * Reduce a single status value into the canonical Session.status.
 * Returns `null` for unrecognised values.
 *
 * Accepts both a bare string and OpenCode's `SessionStatus` object shape
 * (`{type: 'idle' | 'busy' | 'retry'}`). `idle` maps to `done` and `retry`
 * — a provider backoff *within* a turn — maps to `busy`, matching
 * `turnRunning` on the backend.
 */
function normaliseStatus(raw: unknown): SessionMetadata['status'] | null {
  let s: string | undefined;
  if (typeof raw === 'string') {
    s = raw;
  } else if (raw && typeof raw === 'object') {
    const t = (raw as Record<string, unknown>).type;
    if (typeof t === 'string') s = t;
  }
  if (!s) return null;
  if (s === 'idle') return 'done';
  if (s === 'retry') return 'busy';
  if (s === 'waiting' || s === 'busy' || s === 'limited' || s === 'done' || s === 'error' || s === 'interrupted') return s;
  return null;
}

function retryNotice(raw: unknown): SessionMetadata['notice'] | null {
  if (!raw || typeof raw !== 'object') return null;
  const status = raw as Record<string, unknown>;
  if (status.type !== 'retry' || typeof status.message !== 'string') return null;
  return {
    kind: 'retry',
    message: status.message,
    retryAt: typeof status.next === 'number' ? status.next : 0,
    attempt: typeof status.attempt === 'number' ? status.attempt : 0,
  };
}

/**
 * Dispatch table for SSE events. Lives in a helper so the main
 * reducer keeps the action-shape switch flat and readable.
 */
export function reduceSseEvent(state: SessionView, event: SseEvent): SessionView {
  const props = event.properties || {};
  switch (event.type) {
    case 'server.connected':
      return reduceBackgroundTaskCount(state, backgroundSnapshotCount(props.backgroundTasks, state.sessionId));
    case 'ocman.session.background':
      return reduceBackgroundTaskCount(state, props.count);
    case 'message.created':
    case 'message.updated':
      return reduceMessageSnapshot(state, event);
    case 'message.part.updated':
      return reducePartSnapshot(state, props);
    case 'message.part.delta':
      return reducePartDelta(state, props);
    case 'session.status':
      return reduceSessionStatus(state, props);
    case 'session.idle':
      return reduceSessionIdle(state);
    case 'permission.asked':
      return reducePermissionAsked(state, event);
    case 'permission.replied':
      return reducePermissionReplied(state, props);
    case 'ocman.permission.pending':
      return reducePermissionPending(state, props);
    case 'ocman.permission.checking':
      return reducePermissionChecking(state, props);
    case 'ocman.permission.flagged':
      return reducePermissionFlagged(state, props);
    case 'ocman.permission.auto-approved':
    case 'ocman.permission.approved':
      return reducePermissionAutoApproved(state, props);
    case 'question.asked':
      return reduceQuestionAsked(state, event);
    case 'question.replied':
    case 'question.rejected':
      return reduceQuestionCleared(state, props);
    default:
      // Unknown / named-channel events may still carry a message or
      // part payload. OpenCode has used raw `tool` named events for
      // live tool-start snapshots; dropping those means the user sees
      // no tool block until completion. Recover the known payload
      // shapes here while still treating truly unknown events as no-ops.
      if (props.info) {
        const next = reduceMessageSnapshot(state, event);
        if (next !== state) return next;
      }
      if (props.part) {
        const next = reducePartSnapshot(state, props);
        if (next !== state) return next;
      }
      if (typeof props.id === 'string' && (typeof props.messageID === 'string' || typeof props.messageId === 'string')) {
        const next = reducePartSnapshot(state, { part: props });
        if (next !== state) return next;
      }
      return state;
  }
}

function reduceMessageSnapshot(state: SessionView, event: SseEvent): SessionView {
  // Reuse the existing wire-shape extractor — it normalises message
  // role defaults, truncates oversized fields, and filters lifecycle
  // part types.
  const parsed: Record<string, unknown> = {
    type: event.type,
    properties: event.properties,
  };
  const extracted = extractMessageFromEvent(parsed, state.sessionId);
  if (!extracted) return state;

  const nextMessages = upsertMessage(state.messages, extracted.message);
  let nextParts = state.parts;
  // The ownership map mutates in-place inside upsertSnapshotPart's
  // helpers; clone once up front so the reducer stays pure.
  const ownedFields = state._deltaOwnedFields;
  for (const part of extracted.parts) {
    nextParts = upsertSnapshotPart(nextParts, part, ownedFields);
  }

  // Deliberately does NOT touch session.status. The backend settles
  // lifecycle status from the agent's own turn (db.SettleSessionStatus)
  // and delivers it via the REST snapshot and `session.status` events;
  // message shape only ever decided *which* terminal state, so
  // re-deriving it here just overwrote the authoritative value with a
  // guess. The "just sent, awaiting the assistant" affordance lives in
  // useSessionActions' awaitingAssistantResponse flag instead.
  if (nextMessages === state.messages && nextParts === state.parts) {
    return state;
  }
  return {
    ...state,
    messages: nextMessages,
    parts: nextParts,
    _deltaOwnedFields: ownedFields,
  };
}

function reducePartSnapshot(state: SessionView, props: Record<string, unknown>): SessionView {
  const rawPart = props.part as Record<string, unknown> | undefined
    || (typeof props.id === 'string' ? props : undefined);
  const messageId = (rawPart?.messageID as string | undefined) || (rawPart?.messageId as string | undefined);
  if (!rawPart || typeof rawPart.id !== 'string' || typeof messageId !== 'string') {
    return state;
  }
  const partType = rawPart.type as string | undefined;
  if (partType === 'step-start' || partType === 'step-finish' || partType === 'snapshot') {
    return state;
  }
  const part: Part = {
    id: rawPart.id,
    messageId,
    sessionId: (rawPart.sessionID as string) || state.sessionId,
    data: rawPart as unknown as string,
  };
  const nextParts = upsertSnapshotPart(state.parts, part, state._deltaOwnedFields);

  // `message.part.updated` can lead `message.created` for active
  // tools. Keep a minimal assistant message so the converter has a
  // parent row to attach the running tool block to immediately;
  // the later message snapshot will replace it with authoritative
  // metadata (tokens, finish reason, timestamps).
  let nextMessages = state.messages;
  if (!state.messages.some((m) => m.id === messageId)) {
    const rawTime = rawPart.time as Record<string, unknown> | undefined;
    const created = typeof rawTime?.created === 'number' ? rawTime.created : Date.now();
    const stub: Message = {
      id: messageId,
      sessionId: (rawPart.sessionID as string) || state.sessionId,
      timeCreated: created,
      data: { role: 'assistant' },
    };
    nextMessages = upsertMessage(state.messages, stub);
  }

  // A running/pending tool part is a *live* signal, not a message
  // snapshot: it says a turn is in flight right now. Unlike
  // reduceMessageSnapshot (which used to re-derive a terminal state from
  // stored message shape and had to stop), this is what keeps the badge
  // busy between `session.status` events — do not remove it.
  let nextSession = state.session;
  const rawState = rawPart.state as Record<string, unknown> | undefined;
  const status = rawState?.status;
  if ((status === 'running' || status === 'pending') && state.session && state.session.status !== 'busy') {
    nextSession = { ...state.session, status: 'busy' };
  }

  // If this snapshot is the question tool part that backs the current
  // pending prompt, and it has now been answered (e.g. the user
  // replied in the OpenCode CLI rather than in ocman), dismiss the
  // prompt. OpenCode doesn't reliably emit `question.replied` for
  // out-of-band answers, so the resolved tool part is our signal.
  let nextPendingQuestion = state.pendingQuestion;
  if (state.pendingQuestion) {
    const answeredId = answeredQuestionRequestId(part);
    if (answeredId && answeredId === state.pendingQuestion.requestId) {
      nextPendingQuestion = null;
    }
  }

  if (
    nextParts === state.parts
    && nextMessages === state.messages
    && nextSession === state.session
    && nextPendingQuestion === state.pendingQuestion
    && !(state._statusFresh === false && (status === 'running' || status === 'pending'))
  ) {
    return state;
  }
  return {
    ...state,
    messages: nextMessages,
    parts: nextParts,
    session: nextSession,
    pendingQuestion: nextPendingQuestion,
    _statusFresh: status === 'running' || status === 'pending' ? true : state._statusFresh,
    _statusRevision: status === 'running' || status === 'pending' ? (state._statusRevision ?? 0) + 1 : state._statusRevision,
  };
}

function reducePartDelta(state: SessionView, props: Record<string, unknown>): SessionView {
  const partId = props.partID as string | undefined;
  const messageId = props.messageID as string | undefined;
  const delta = props.delta as string | undefined;
  const field = (props.field as string) || 'text';
  if (!partId || !messageId || !delta) return state;
  if (!STREAMING_FIELDS.has(field)) return state;

  // Clone the ownership map for immutability.
  const ownedFields = new Map(
    Array.from(state._deltaOwnedFields, ([k, v]) => [k, new Set(v)] as [string, Set<string>]),
  );

  // Synthesise a stub assistant message if the owning message hasn't
  // arrived yet — keeps the converter happy when deltas race
  // message.created.
  let nextMessages = state.messages;
  if (!state.messages.some((m) => m.id === messageId)) {
    const stub: Message = {
      id: messageId,
      sessionId: (props.sessionID as string) || state.sessionId,
      timeCreated: Date.now(),
      data: { role: 'assistant' },
    };
    nextMessages = upsertMessage(state.messages, stub);
  }

  const nextParts = applyDelta(state.parts, {
    partId,
    messageId,
    sessionId: (props.sessionID as string) || state.sessionId,
    field,
    delta,
  }, ownedFields);

  // The streaming part implies the session is busy — but only flip
  // if we know it isn't already. Mirrors the legacy behaviour where
  // delta arrival drives the "running" indicator before the next
  // session.status event lands. Same rationale as reducePartSnapshot:
  // this is a live signal, not a re-derivation from stored message
  // shape, and removing it leaves the badge stale mid-turn.
  let nextSession = state.session;
  if (state.session && state.session.status !== 'busy') {
    nextSession = { ...state.session, status: 'busy' };
  }

  return {
    ...state,
    messages: nextMessages,
    parts: nextParts,
    session: nextSession,
    _deltaOwnedFields: ownedFields,
    _statusFresh: true,
    _statusRevision: (state._statusRevision ?? 0) + 1,
  };
}

function reduceSessionStatus(state: SessionView, props: Record<string, unknown>): SessionView {
  const status = normaliseStatus(props.status);
  if (!status) return state;
  const notice = retryNotice(props.status);
  const rawStatus = props.status && typeof props.status === 'object' ? props.status as Record<string, unknown> : props;
  const resetsAt = status === 'limited' && typeof rawStatus.resetsAt === 'number' ? rawStatus.resetsAt : 0;
  const clearRetryNotice = !notice && ['retry', 'rate_limit', 'provider_overloaded'].includes(state.session?.notice?.kind ?? '');
  // Treat `session.status` with status === idle the same as
  // session.idle — also flag a refetch so the host hook reconciles
  // any missed content.
  const refetch = isSessionStatusIdle({ properties: props });
  // Even the same status is a new live observation that fences older list reads.
  return {
    ...state,
    session: state.session ? {
      ...state.session,
      status,
      resetsAt,
      ...(notice ? { notice } : clearRetryNotice ? { notice: undefined } : {}),
    } : state.session,
    _refetchRequested: refetch || state._refetchRequested,
    ...(refetch ? { backgroundTaskCount: 0 } : {}),
    _statusFresh: true,
    _statusRevision: (state._statusRevision ?? 0) + 1,
  };
}

function reduceSessionIdle(state: SessionView): SessionView {
  let nextSession = state.session;
  if (state.session && state.session.status !== 'done') {
    nextSession = { ...state.session, status: 'done', resetsAt: 0 };
  }
  return {
    ...state,
    session: nextSession,
    backgroundTaskCount: 0,
    _refetchRequested: true,
    _statusFresh: true,
    _statusRevision: (state._statusRevision ?? 0) + 1,
  };
}
