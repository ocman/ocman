// Platform-agnostic fold of Ocman conversation events into the page's
// SessionView (design §9.2). Components keep reading SessionView unchanged.

import { reduceBackgroundTaskCount } from '../sessionBackgroundTasks';
import { initialSessionView, type SessionMetadata, type SessionView } from '../sessionViewTypes';
import { upsertMessage } from '../sessionViewParts';
import { applyAnchoredDelta, removeMessage, removePart, upsertPart } from './conversationParts';
import { applyPromptOverlay, askPrompt, judgePrompt, resolvePrompt } from './conversationPrompts';
import { coalesceDeltas } from './deltaBatch';
import type {
  ChildStatus, ChildUpserted, LiveOverlay, OceEnvelope, OceEvent, SessionStatusEvent, SessionUpdated,
} from './events';

// Backoff notices describe the attempt in flight; any newer level supersedes them.
const TRANSIENT_NOTICES = new Set(['retry', 'rate_limit', 'provider_overloaded']);

function applyStatus(view: SessionView, e: SessionStatusEvent): SessionView {
  let session = view.session;
  if (session) {
    let notice = session.notice;
    if (e.notice === null) notice = undefined;
    else if (e.notice) notice = e.notice;
    else if (TRANSIENT_NOTICES.has(notice?.kind ?? '')) notice = undefined;
    session = { ...session, status: e.status, resetsAt: e.status === 'limited' ? e.resetsAt ?? 0 : 0, notice };
  }
  return {
    ...view,
    session,
    _statusFresh: true,
    _statusRevision: (view._statusRevision ?? 0) + 1,
  };
}

function patchMeta(view: SessionView, { patch }: SessionUpdated): SessionView {
  if (!view.session) return view;
  const next: Partial<SessionMetadata> = {};
  if (patch.title !== undefined) next.title = patch.title;
  if (patch.summaryAdditions !== undefined) next.summaryAdditions = patch.summaryAdditions;
  if (patch.summaryDeletions !== undefined) next.summaryDeletions = patch.summaryDeletions;
  if (patch.summaryFiles !== undefined) next.summaryFiles = patch.summaryFiles;
  if (patch.shareUrl !== undefined) next.shareUrl = patch.shareUrl;
  if (patch.archived !== undefined) next.archived = patch.archived;
  if (patch.defaultAgent !== undefined) next.defaultAgent = patch.defaultAgent;
  if (patch.defaultModel !== undefined) next.defaultModel = patch.defaultModel;
  if (Object.keys(next).length === 0) return view;
  return { ...view, session: { ...view.session, ...next } };
}

function upsertChild(view: SessionView, { child }: ChildUpserted): SessionView {
  const idx = view.sessionTree.findIndex((s) => s.id === child.id);
  if (child.deleted) {
    return idx < 0 ? view : { ...view, sessionTree: view.sessionTree.filter((s) => s.id !== child.id) };
  }
  if (idx < 0) return { ...view, _refetchRequested: true };
  const sessionTree = [...view.sessionTree];
  sessionTree[idx] = { ...sessionTree[idx], title: child.title || sessionTree[idx].title, timeUpdated: child.timeUpdated || sessionTree[idx].timeUpdated };
  return { ...view, sessionTree };
}

function patchChild(view: SessionView, e: ChildStatus): SessionView {
  if (!view.sessionTree.some((s) => s.id === e.childId && s.status !== e.status)) return view;
  return { ...view, sessionTree: view.sessionTree.map((s) => (s.id === e.childId ? { ...s, status: e.status } : s)) };
}

function applyOverlay(view: SessionView, overlay: LiveOverlay): SessionView {
  let s = applyStatus(view, overlay.status);
  s = applyPromptOverlay(s, overlay.prompts ?? [], overlay.judge ?? []);
  s = reduceBackgroundTaskCount(s, overlay.background?.count ?? 0, overlay.background?.tasks);
  let messages = s.messages;
  for (const message of overlay.inflight?.messages ?? []) messages = upsertMessage(messages, message);
  s = { ...s, messages };
  for (const part of overlay.inflight?.parts ?? []) s = upsertPart(s, part, false);
  if (s.session && s.session.liveConnection !== overlay.liveConnection) {
    s = { ...s, session: { ...s.session, liveConnection: overlay.liveConnection } };
  }
  return s;
}

type Handlers = { [T in OceEvent['type']]?: (s: SessionView, e: Extract<OceEvent, { type: T }>) => SessionView };
const requestRefetch = (s: SessionView): SessionView => ({ ...s, _refetchRequested: true });

// turn.started, queue.changed, changes.dirty, session.error and stream.error
// carry no view state; the stream client routes the side-channel ones.
const HANDLERS: Handlers = {
  'stream.hello': (s, e) => (e.resumed || !e.overlay ? s : applyOverlay(s, e.overlay)),
  'message.upserted': (s, e) => ({ ...s, messages: upsertMessage(s.messages, e.message) }),
  'message.removed': (s, e) => removeMessage(s, e.messageId),
  'part.upserted': (s, e) => upsertPart(s, e.part, e.final),
  'part.removed': (s, e) => removePart(s, e.partId),
  'part.delta': applyAnchoredDelta,
  'session.status': applyStatus,
  'turn.ended': requestRefetch,
  'prompt.asked': (s, e) => askPrompt(s, e.prompt),
  'prompt.judge': judgePrompt,
  'prompt.resolved': resolvePrompt,
  'background.changed': (s, e) => reduceBackgroundTaskCount(s, e.count, e.tasks),
  'session.updated': patchMeta,
  'child.upserted': upsertChild,
  'child.status': patchChild,
  'history.rewritten': requestRefetch,
  'stream.reset': requestRefetch,
};

function reduceEvent(s: SessionView, e: OceEvent): SessionView {
  // Unknown types are ignored (forward compatibility), including prototype keys.
  if (!Object.prototype.hasOwnProperty.call(HANDLERS, e.type)) return s;
  return (HANDLERS[e.type] as (s: SessionView, e: OceEvent) => SessionView)(s, e);
}

function trackLive(view: SessionView, e: OceEvent): SessionView {
  const cold = e.type === 'stream.hello' && !e.resumed;
  const base = cold ? undefined : view._live;
  let live = base ?? { ...initialSessionView(view.sessionId), session: view.session };
  if (!live.session && view.session) live = { ...live, session: view.session };
  live = reduceEvent(live, e);
  if (e.type === 'part.upserted') {
    const finals = new Set(live._liveFinalParts);
    if (e.final) finals.add(e.part.id); else finals.delete(e.part.id);
    live = { ...live, _liveFinalParts: finals };
  } else if (live !== base && live._liveFinalParts?.size && (e.type === 'part.removed' || e.type === 'message.removed')) {
    const present = new Set(live.parts.map((p) => p.id));
    live = { ...live, _liveFinalParts: new Set([...live._liveFinalParts ?? []].filter((id) => present.has(id))) };
  }
  if (e.type === 'turn.ended' || e.type === 'history.rewritten'
    || (e.type === 'session.status' && e.provisional && e.status === 'done')) {
    live = { ...live, messages: [], parts: [], _deltaOwnedFields: new Map(), _liveFinalParts: undefined };
  }
  // Do not create bookkeeping for events that changed no live state.
  if (live === base || (!base && !cold && live.messages.length === 0 && live.parts.length === 0
    && live._statusRevision === undefined && !live.pendingPermission && !live.pendingQuestion
    && !live.backgroundTaskCount && !live.checkingPermissionId && !live.judgeStartsAt)) return view;
  return { ...view, _live: live };
}

/** Pure fold of a batch of envelopes. Returns `view` itself when nothing applies. */
export function reduceConversation(view: SessionView, batch: readonly OceEnvelope[]): SessionView {
  let s = view;
  for (const env of coalesceDeltas(batch)) {
    if (env.sessionId !== s.sessionId || !env.event) continue;
    s = trackLive(reduceEvent(s, env.event), env.event);
  }
  return s;
}
