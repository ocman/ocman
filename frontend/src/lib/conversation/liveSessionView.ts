// The page reducer: REST loads and client actions go through the session
// reducer; conversation stream batches go through reduceConversation.

import { timeSseBatch } from '../perfMonitor';
import { reduceSessionView } from '../sessionReducer';
import type { SessionAction, SessionView } from '../sessionViewTypes';
import { reduceConversation } from './conversationReducer';
import { upsertPart } from './conversationParts';
import type { OceEnvelope, OcePart } from './events';
import { decode, encode, keepLongerStreamedFields, upsertMessage, upsertSnapshotPart } from '../sessionViewParts';
import { findIndexById, replaceAt } from './partIndex';

// A non-final live part only contributes streamed text. REST owns its status,
// timing and other metadata, which may be newer than the live copy.
function restoreStreamedFields(view: SessionView, live: OcePart): SessionView {
  const idx = findIndexById(view.parts, live.id);
  if (idx < 0) return upsertPart(view, live, false);
  const next = keepLongerStreamedFields(decode(view.parts[idx]), decode(live as never));
  return { ...view, parts: replaceAt(view.parts, idx, encode(view.parts[idx], next)) };
}

// The live ledger already folded the fenced prompts; replaying an ask would
// resurrect a prompt answered while the recovery read was in flight.
function fencedReplay(sinceFence: OceEnvelope[], hasLive: boolean): OceEnvelope[] {
  return hasLive ? sinceFence.filter((e) => e.event.type !== 'prompt.asked') : sinceFence;
}

export type LiveSessionAction = SessionAction | { type: 'oceBatch'; envelopes: OceEnvelope[] }
  /** `covered`: the read includes streamed content up to its cursor. */
  | { type: 'recover'; view: SessionView; sinceFence: OceEnvelope[]; restoreLiveFields?: boolean; covered?: boolean }
  | LoadAction;

/** A REST read plus the stream events it does not cover. */
type LoadAction = Extract<SessionAction, { type: 'load' }> & { sinceFence?: OceEnvelope[] };

type RecoverAction = Extract<LiveSessionAction, { type: 'recover' }>;

function recover(view: SessionView, action: RecoverAction): SessionView {
  let incoming = action.view;
  // On navigation back to an active turn, REST may lag cached deltas for
  // parts it still contains. Keep those fields, not entries missing in REST.
  // Rewrites and non-resumable reconnects never use this cache-restoration rule.
  if (action.restoreLiveFields && incoming.session?.status === 'busy' && view.sessionId === incoming.sessionId) {
    const known = new Map(view.parts.map((p) => [p.id, p]));
    const retained = new Set(incoming.parts.map((p) => p.id));
    incoming = { ...incoming, _deltaOwnedFields: new Map([...view._deltaOwnedFields].filter(([id]) => retained.has(id))), parts: incoming.parts.map((p) => {
      const old = known.get(p.id);
      return old ? upsertSnapshotPart([old], p, view._deltaOwnedFields)[0] : p;
    }) };
  }
  // Replace REST-owned history, not the stream's prompt/task ledger or live
  // turn. Anchored deltas and upserts are idempotent when replayed below.
  const live = view.sessionId === incoming.sessionId ? view._live : undefined;
  if (live) {
    incoming = { ...incoming, _live: live,
      pendingPermission: live.pendingPermission, pendingQuestion: live.pendingQuestion,
      judgeStartsAt: live.judgeStartsAt, checkingPermissionId: live.checkingPermissionId, judgeReasoning: live.judgeReasoning,
      backgroundTaskCount: live.backgroundTaskCount, backgroundTasks: live.backgroundTasks,
      _deltaOwnedFields: live._deltaOwnedFields,
      session: incoming.session && live.session && live._statusRevision !== undefined ? { ...incoming.session, status: live.session.status,
        notice: live.session.notice, resetsAt: live.session.resetsAt, liveConnection: live.session.liveConnection } : incoming.session };
    // A composed read already holds the live turn up to its cursor.
    for (const message of action.covered ? [] : live.messages) incoming = { ...incoming, messages: upsertMessage(incoming.messages, message) };
    for (const part of action.covered ? [] : live.parts) {
      const oce = { ...part, data: typeof part.data === 'string' ? JSON.parse(part.data) : part.data };
      incoming = live._liveFinalParts?.has(part.id) ? upsertPart(incoming, oce, true) : restoreStreamedFields(incoming, oce);
    }
  }
  return reduceConversation({ ...incoming, _statusRevision: view._statusRevision,
    _refetchRequested: false, _stale: undefined }, fencedReplay(action.sinceFence, !!live));
}

// A load is the reconcile that heals stale parts. Stream bookkeeping
// survives it for the same session.
function afterLoad(view: SessionView, next: SessionView, action: LoadAction): SessionView {
  const same = view.sessionId === next.sessionId;
  const loaded = { ...next, _stale: undefined, _live: same ? view._live : undefined };
  // Events after the read's cursor are newer than the read; they win again.
  return same && action.sinceFence?.length ? reduceConversation(loaded, fencedReplay(action.sinceFence, !!loaded._live)) : loaded;
}

export function reduceLiveSessionView(view: SessionView, action: LiveSessionAction): SessionView {
  if (action.type === 'recover') return recover(view, action);
  if (action.type === 'oceBatch') {
    return timeSseBatch(action.envelopes.length, () => reduceConversation(view, action.envelopes),
      (out) => [out.messages.length, out.parts.length]);
  }
  const next = reduceSessionView(view, action);
  if (action.type !== 'load' || next === view) return next;
  return afterLoad(view, next, action);
}
