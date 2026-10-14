// Pure reducer for the SessionDetail page.
//
// Owns the *only* write path into the page's `SessionView`. REST
// snapshots arrive via `load` actions; SSE events arrive via `sse`
// actions; the action table maps each event type to a deterministic
// state transition. No caching, no length heuristics, no
// reparent-temp-id dance — see spec/sse-rewrite/architecture.md for
// the full design rationale.
//
// Field ownership rule (the only subtle bit):
//
//   Once a `message.part.delta` has been observed for a (partId,
//   field) pair, snapshots can no longer overwrite that field. Every
//   other field is owned by snapshots. This replaces the old length-
//   comparison heuristic with a deterministic ownership table.
//
// The reducer is intentionally allocation-free for the no-op path
// (unknown event, wrong sessionId, no-op state change) — the
// `useSession` hook calls `dispatch` per SSE event and React will
// short-circuit re-renders when the state reference is identical.

import type { Message, Part } from './api';
import type { SessionAction, SessionView, SseEvent } from './sessionViewTypes';
import { upsertMessage, upsertSnapshotPart } from './sessionViewParts';
import { reduceSseEvent } from './sessionSseReducer';

export type {
  AutoApprovedNoticePayload, AutoRejectedNoticePayload, SessionAction, SessionMetadata, SessionView, SseEvent,
} from './sessionViewTypes';
export { initialSessionView } from './sessionViewTypes';
export { seedDeltaOwnedFields } from './sessionViewParts';

/**
 * Resolve the session id an event targets. Returns null when the
 * event lacks one (legacy payloads — treated as "current session"
 * by the caller). Looks in the canonical places OpenCode uses.
 */
function eventSessionId(event: SseEvent): string | null {
  const props = event.properties;
  if (!props) return null;
  if (typeof props.sessionID === 'string') return props.sessionID;
  const info = props.info as Record<string, unknown> | undefined;
  if (info && typeof info.sessionID === 'string') return info.sessionID;
  const part = props.part as Record<string, unknown> | undefined;
  if (part && typeof part.sessionID === 'string') return part.sessionID;
  return null;
}

/**
 * Reconcile-mode load: merge `incoming` into `state` such that
 * messages and parts the server returns supersede their in-memory
 * counterparts (by id), but in-memory entries the server doesn't
 * return are preserved.
 *
 * Why this exists: OpenCode's database write lags its SSE emission
 * during active streaming. If we wholesale-replace state with a
 * refetch response that hasn't caught up, we transiently wipe live
 * content (the streamed bash output, the just-arrived tool block).
 * Reconciling instead keeps everything visible.
 *
 * Snapshot-vs-delta ownership: streaming fields (`text`,
 * `state.output`) keep their local accumulated value when the
 * server's snapshot of the same part hasn't caught up — same rule
 * as for live `message.part.updated` events.
 */
function reconcileLoad(state: SessionView, incoming: SessionView, statusRevision?: number): SessionView {
  if (state.sessionId !== incoming.sessionId) {
    // The incoming data is for a different session than the one currently
    // displayed. This happens when a doFetch from a previous navigation
    // resolves after the user has already switched sessions. Applying it
    // wholesale would paint the wrong session's messages into the current
    // view and, via the cache-mirror effect, corrupt the cache for the
    // current session. Drop the stale data entirely.
    return state;
  }

  // Messages: server's data wins per-id; in-memory-only entries
  // survive. Sort by timeCreated to keep order stable.
  const messageById = new Map<string, Message>();
  for (const m of state.messages) messageById.set(m.id, m);
  for (const m of incoming.messages) messageById.set(m.id, m);
  const mergedMessages = Array.from(messageById.values()).sort(
    (a, b) => a.timeCreated - b.timeCreated,
  );

  // Parts: server's data wins per-id, but each part flows through
  // `upsertSnapshotPart` so the delta-ownership rule applies. This
  // is the same code path SSE snapshots use — keeping the merge
  // logic single-source-of-truth.
  let mergedParts = state.parts;
  const ownedFields = state._deltaOwnedFields;
  for (const part of incoming.parts) {
    mergedParts = upsertSnapshotPart(mergedParts, part, ownedFields);
  }

  // Session metadata: take the server's value (it's the
  // authoritative header — title, status, agents, etc.). When the
  // server didn't return a session at all (defensive), fall back
  // to the current one.
  // A turn seen starting after the request began outranks the snapshot's
  // status. Terminal kinds (done vs error) stay the snapshot's call.
  const liveStatusWins = statusRevision !== undefined && statusRevision !== (state._statusRevision ?? 0)
    && state.session?.status === 'busy' && !!incoming.session;
  const nextSession = liveStatusWins
    ? { ...incoming.session!, status: state.session!.status, resetsAt: state.session!.resetsAt, notice: state.session!.notice }
    : incoming.session ?? state.session;

  // Prompts: prefer SSE-set in-memory state over the REST snapshot.
  // The REST response's pendingPermission/pendingQuestion fields are
  // boolean flags derived from the DB, which lags SSE by hundreds of
  // ms. If the SSE stream has already delivered permission.asked or
  // question.asked, we must not overwrite it with the stale REST
  // value. Only clear when the in-memory state is null (nothing to
  // preserve) and the REST response also has no pending prompt.
  const nextPendingPermission = state.pendingPermission !== null
    ? state.pendingPermission
    : incoming.pendingPermission;
  const nextPendingQuestion = state.pendingQuestion !== null
    ? state.pendingQuestion
    : incoming.pendingQuestion;
  return {
    ...state,
    sessionId: incoming.sessionId,
    session: nextSession,
    _statusFresh: incoming.session && !liveStatusWins ? incoming._statusFresh : state._statusFresh,
    sessionTree: incoming.sessionTree,
    messages: mergedMessages,
    parts: mergedParts,
    pendingPermission: nextPendingPermission,
    pendingQuestion: nextPendingQuestion,
    _deltaOwnedFields: ownedFields,
    _refetchRequested: false,
  };
}

/**
 * Pure reducer. Returns the same `state` reference when the action
 * is a no-op so React skips re-renders.
 */
export function reduceSessionView(state: SessionView, action: SessionAction): SessionView {
  switch (action.type) {
    case 'load': {
      if (action.mode === 'reconcile') {
        // Reconcile-mode load: server data wins on matching ids,
        // but in-memory entries the server doesn't return are
        // preserved. This protects live-streamed messages/parts
        // from being wiped by a refetch whose response hasn't yet
        // caught up with the SSE stream (OpenCode's db write lags
        // the event emission by a few hundred ms during active
        // streaming).
        //
        // `_deltaOwnedFields` is preserved so streaming
        // delta-owned fields (text, state.output) keep their local
        // accumulated value if the snapshot's data is older.
        return reconcileLoad(state, action.view, action.statusRevision);
      }
      // Wholesale replacement. Preserve sessionId from the new view
      // so a load against a different session can't accidentally
      // mix state. Reset all reducer-internal bookkeeping — the
      // server state is now authoritative.
      return {
        ...action.view,
        _deltaOwnedFields: action.view._deltaOwnedFields,
        _refetchRequested: false,
        // Monotonic, so fences taken before the replacement stay comparable.
        _statusRevision: state._statusRevision,
      };
    }
    case 'clearPrompt': {
      if (action.kind === 'permission') {
        if (!state.pendingPermission) return state;
        if (state.pendingPermission.permissionId !== action.id) return state;
        return { ...state, pendingPermission: null };
      }
      if (action.kind === 'question') {
        if (!state.pendingQuestion) return state;
        if (state.pendingQuestion.requestId !== action.id) return state;
        return { ...state, pendingQuestion: null };
      }
      return state;
    }
    case 'setPendingPermission': {
      // Session guard, mirroring the `sse` case below. The reverse-sync
      // fetch that dispatches this outlives a navigation, so without it
      // a late response for session A injects A's prompt into B and
      // disables B's composer behind a dialog it can never answer.
      // An empty sessionId is a legacy payload meaning "this session";
      // ownerIds carries the page session's subagent descendants, whose
      // prompts deliberately surface on the parent row.
      const owner = action.permission.sessionId;
      if (owner && owner !== state.sessionId && !action.ownerIds?.includes(owner)) return state;
      if (state.pendingPermission?.permissionId === action.permission.permissionId) return state;
      return {
        ...state,
        pendingPermission: action.permission,
        judgeStartsAt: null,
        judgeReasoning: null,
      };
    }
    case 'patchSession': {
      if (!state.session) return state;
      return { ...state, session: { ...state.session, ...action.patch } };
    }
    case 'sse': {
      const event = action.event;
      const evtSid = eventSessionId(event);
      // Events that explicitly belong to a different session are
      // dropped here. The legacy "subagent bubbling" path is no
      // longer the reducer's concern; the hook routes those events
      // by composing a second `useSession(subagentId)`.
      if (evtSid && evtSid !== state.sessionId) return state;
      return reduceSseEvent(state, event);
    }
    case 'addNotice': {
      // Use the OpenCode permission ID as a stable key so re-dispatching
      // the same approval (e.g. on reconcile refetch) is a no-op.
      // Previously this was the judge session ID, but those sessions are
      // now deleted as soon as the verdict is parsed — permissionId is
      // the only durable, unique handle for a given approval.
      const stableKey = `ocman-notice-${action.notice.permissionId}`;
      if (state.messages.some((m) => m.id === stableKey)) {
        return state; // already present — deduplicate
      }
      const ts = action.notice.approvedAt ?? Date.now();
      const noticeMsg: Message = {
        id: stableKey,
        sessionId: state.sessionId,
        timeCreated: ts,
        data: { role: 'notice' },
      };
      const noticePart: Part = {
        id: `${stableKey}-part`,
        messageId: stableKey,
        sessionId: state.sessionId,
        timeCreated: ts,
        data: JSON.stringify({
          type: 'auto-approved',
          permission: action.notice.permission,
          patterns: action.notice.patterns,
          reasoning: action.notice.reasoning ?? '',
          approvedBy: action.notice.approvedBy ?? 'ai',
          reply: action.notice.reply,
          metadata: action.notice.metadata,
          askedAt: action.notice.askedAt,
          approvedAt: ts,
        }),
      };
      // Insert the notice into the sorted message list so it appears
      // at the correct chronological position rather than always at
      // the bottom. upsertMessage does a sorted insert by timeCreated.
      return {
        ...state,
        messages: upsertMessage(state.messages, noticeMsg),
        parts: [...state.parts, noticePart],
      };
    }
    default:
      return state;
  }
}
