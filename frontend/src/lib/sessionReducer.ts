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

import type { Message, Part, SessionDetail } from './api';
import type { PendingPermission } from './sseHelpers';
import type { PendingQuestion } from '../components/session/QuestionPrompt';
import {
  answeredQuestionRequestId,
  extractMessageFromEvent,
  extractPendingPermission,
  extractPendingQuestion,
  isSessionStatusIdle,
} from './sseHelpers';

/** Session metadata as held by the page, including SessionDetail extras. */
export type SessionMetadata = SessionDetail['session'] & {
  defaultAgent?: string;
  defaultModel?: string;
  warnings?: SessionDetail['warnings'];
};

/**
 * Streaming fields are appendable string values reachable by a
 * dotted path on `part.data`. They're the only fields a delta can
 * target; everything else is snapshot-owned by definition.
 *
 * Mirror of the legacy `STREAMING_FIELDS` set — kept here so the
 * reducer is self-contained and partReducer.ts can be deleted.
 */
const STREAMING_FIELDS: ReadonlySet<string> = new Set([
  'text',
  'state.output',
]);

/**
 * The single piece of state the SessionDetail page owns.
 *
 * `_deltaOwnedFields` and `_refetchRequested` are reducer-internal
 * bookkeeping prefixed with `_` so consumers don't accidentally
 * depend on them. They survive `sse` actions but are cleared on
 * `load` (the freshly-fetched server state is authoritative).
 */
export interface SessionView {
  /** Active session id this view describes. Mismatched SSE events
   *  are dropped against this id. */
  sessionId: string;
  /** Session metadata header (status, agent, timing, ...). `null`
   *  until the first `load` lands. */
  session: SessionMetadata | null;
  /** Ancestors and descendants returned with the detail snapshot. */
  sessionTree: NonNullable<SessionDetail['sessionTree']>;
  /** Chronological messages keyed by id. */
  messages: Message[];
  /** Flat parts list, in arrival order. The converter buckets by
   *  messageId at render time. */
  parts: Part[];
  /** Permission prompt awaiting reply, or null. */
  pendingPermission: PendingPermission | null;
  /** Question prompt awaiting reply, or null. */
  pendingQuestion: PendingQuestion | null;
  /** Per-part set of fields that have ever received a streaming
   *  delta. Snapshots must leave those fields alone. */
  _deltaOwnedFields: Map<string, Set<string>>;
  /** True when the reducer wants the host hook to refetch
   *  `/api/session/{id}` (currently only set by `session.idle`).
   *  Consumers must reset this flag once the refetch has been
   *  scheduled. */
  _refetchRequested: boolean;
  /**
   * Set by the backend-emitted `ocman.permission.pending` SSE event.
   * Unix-ms timestamp of when the LLM judge will start (after the
   * configured delay). The UI counts down to this time. Cleared when
   * `ocman.permission.checking` arrives (judge has started) or when
   * the permission is cleared.
   */
  judgeStartsAt: number | null;
  /**
   * Set by the backend-emitted `ocman.permission.checking` SSE event.
   * Contains the permission ID currently being evaluated by the
   * server-side LLM judge. Cleared when a `permission.replied` or
   * `ocman.permission.auto-approved` event arrives for the same ID.
   * The UI uses this to show a "checking" spinner on the prompt.
   */
  checkingPermissionId: string | null;
  /**
   * The judge's one-line conclusion (the "reasoning" field of the JSON
   * it emits), surfaced on the prompt so the user can see *why* the AI
   * flagged a permission. Set by `ocman.permission.flagged` and
   * cleared when `pendingPermission` is cleared.
   *
   * The transient OpenCode session that produced this reasoning is
   * deleted by the backend as soon as the verdict is parsed, so the
   * UI no longer offers a "view judge session" link — `judgeReasoning`
   * is the only judge artifact carried in client state.
   */
  judgeReasoning: string | null;
}

/**
 * Parsed SSE event in the shape the reducer accepts. `properties`
 * carries the OpenCode-specific payload (info / part / status /
 * delta / ...).
 */
export interface SseEvent {
  type: string;
  properties?: Record<string, unknown>;
}

/**
 * Payload for a synthetic auto-approve notice injected into the
 * conversation thread. Stored as a Part with type 'auto-approved'
 * attached to a synthetic Message with role 'notice'.
 */
export interface AutoApprovedNoticePayload {
  /** The OpenCode permission ID. Used as the stable notice key so
   *  repeated dispatches (reconcile refetches, SSE replays) dedupe. */
  permissionId: string;
  permission: string;
  patterns: string[];
  /** Judge's one-line conclusion. Optional — empty for legacy
   *  pre-v11 rows or when the model omitted the field. */
  reasoning?: string;
  /** Unix-ms timestamp of when the permission was approved.
   *  When present, used as the notice message's timeCreated so it
   *  sorts into the correct chronological position. */
  approvedAt?: number;
}

/**
 * Payload for a synthetic notice injected into the thread when the
 * AI judge blocks (rejects) a permission request. The human is still
 * shown the original prompt — this notice just records why the AI
 * flagged it so it's visible in the thread history.
 */
export interface AutoRejectedNoticePayload {
  /** The OpenCode permission ID — used as the stable notice key. */
  permissionId: string;
  permission: string;
  patterns: string[];
  reasoning?: string;
  /** Unix-ms timestamp of when the rejection occurred. */
  rejectedAt?: number;
}

export type SessionAction =
  /**
   * Load action. `mode` controls how the new view interacts with
   * the current state:
   *
   * - `replace` (default): wholesale swap. Used on initial mount,
   *   manual reload, and session change — when we have no reason
   *   to trust the in-memory state over the server's response.
   *
   * - `reconcile`: merge. The server's data wins on matching ids
   *   (messages by id, parts by id), but any in-memory entry the
   *   server doesn't return is preserved. Used for refetches
   *   triggered by SSE signals like `session.idle` or
   *   `session.diff`, where OpenCode's database may lag the SSE
   *   stream by a few hundred ms — a wholesale replace would
   *   transiently wipe parts that were just streamed.
   *
   *   `_deltaOwnedFields` is preserved in reconcile mode so live
   *   `state.output` deltas don't get overwritten by stale
   *   snapshots from the refetch.
   */
  | { type: 'load'; view: SessionView; mode?: 'replace' | 'reconcile' }
  | { type: 'sse'; event: SseEvent }
  | { type: 'setPendingPermission'; permission: PendingPermission; ownerIds?: string[] }
  | { type: 'clearPrompt'; kind: 'permission' | 'question'; id: string }
  | { type: 'patchSession'; patch: Partial<SessionMetadata> }
  | { type: 'addNotice'; notice: AutoApprovedNoticePayload }
  | { type: 'addRejectedNotice'; notice: AutoRejectedNoticePayload };

/**
 * Fresh, empty view for a given session id. The hook seeds the
 * reducer with this and waits for a `load` action to populate the
 * actual session metadata.
 */
export function initialSessionView(sessionId: string): SessionView {
  return {
    sessionId,
    session: null,
    sessionTree: [],
    messages: [],
    parts: [],
    pendingPermission: null,
    pendingQuestion: null,
    _deltaOwnedFields: new Map(),
    _refetchRequested: false,
    judgeStartsAt: null,
    checkingPermissionId: null,
    judgeReasoning: null,
  };
}

/**
 * Derive a `_deltaOwnedFields` map from a list of parts by marking
 * every non-empty streaming field as delta-owned. Used by the host
 * hook when seeding the reducer from the per-session cache: the
 * cached parts already carry SSE-accumulated text, but the cache
 * format doesn't persist the ownership map. Without re-seeding it,
 * a reconcile-mode load against a DB-lagging server response would
 * wipe streamed chunks that the server hadn't recorded yet.
 *
 * The rule is conservative — any streaming field with a non-empty
 * string is treated as delta-built. Snapshot-only parts (e.g. tool
 * inputs, step-finish blocks) have no streaming fields and won't be
 * marked. Combined with the longest-wins rule in `upsertSnapshotPart`,
 * this guarantees that streamed text only ever grows, never shrinks.
 */
export function seedDeltaOwnedFields(parts: Part[]): Map<string, Set<string>> {

  const map = new Map<string, Set<string>>();
  for (const part of parts) {
    const data = decode(part);
    let owned: Set<string> | null = null;
    for (const field of STREAMING_FIELDS) {
      const value = getFieldByPath(data, field);
      if (typeof value === 'string' && value.length > 0) {
        if (!owned) owned = new Set<string>();
        owned.add(field);
      }
    }
    if (owned) map.set(part.id, owned);
  }
  return map;
}

/**
 * Decode `part.data` to a plain object. Mirrors the helper in
 * `parsePart`/partReducer's decode — kept private so the reducer
 * is self-contained.
 */
function decode(part: Part): Record<string, unknown> {
  const raw = part.data as unknown;
  if (typeof raw === 'string') {
    try {
      return JSON.parse(raw) as Record<string, unknown>;
    } catch {
      return {};
    }
  }
  if (raw && typeof raw === 'object') return raw as Record<string, unknown>;
  return {};
}

function encode(part: Part, data: Record<string, unknown>): Part {
  return { ...part, data: data as unknown as string };
}

function getFieldByPath(obj: Record<string, unknown>, path: string): unknown {
  if (path.indexOf('.') < 0) return obj[path];
  const segs = path.split('.');
  let cur: unknown = obj;
  for (const seg of segs) {
    if (!cur || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

function setFieldByPath(
  obj: Record<string, unknown>,
  path: string,
  value: unknown,
): Record<string, unknown> {
  const dotIdx = path.indexOf('.');
  if (dotIdx < 0) return { ...obj, [path]: value };
  const parent = path.slice(0, dotIdx);
  const child = path.slice(dotIdx + 1);
  const parentObj = (obj[parent] && typeof obj[parent] === 'object')
    ? obj[parent] as Record<string, unknown>
    : {};
  return { ...obj, [parent]: { ...parentObj, [child]: value } };
}

/** Sorted insert by `timeCreated` ascending, replacing on id match. */
function upsertMessage(prev: Message[], msg: Message): Message[] {
  const idx = prev.findIndex((m) => m.id === msg.id);
  if (idx >= 0) {
    const next = [...prev];
    next[idx] = msg;
    return next;
  }
  const insertAt = prev.findIndex((m) => m.timeCreated > msg.timeCreated);
  if (insertAt < 0) return [...prev, msg];
  return [...prev.slice(0, insertAt), msg, ...prev.slice(insertAt)];
}

/**
 * Apply a snapshot part to the parts list. Streaming fields that
 * have already received deltas use "longest string wins": the
 * accumulated text or output is append-only on the wire, so whichever
 * side has more characters is the more-complete value. Non-streaming
 * fields always take the snapshot.
 *
 * Why longest-wins (and not "local always wins"): the local value is
 * authoritative when SSE has streamed past the DB (the common case
 * — DB lags SSE by hundreds of ms). But the inverse can happen too:
 *   1. The cache mirror restored a part on session revisit, seeding
 *      its delta-owned fields from the cached text. If the user was
 *      away long enough that the server DB has caught up *past* the
 *      cached text, the snapshot is the more-complete value.
 *   2. A mid-stream reconcile fetch (e.g. `session.diff`) can return
 *      a snapshot that leads the SSE stream by a delta or two.
 * In both cases, taking the longer string is strictly safe because
 * streaming fields are append-only — the shorter string is always a
 * prefix of the longer one.
 */
function upsertSnapshotPart(
  parts: Part[],
  snapshot: Part,
  ownedFields: Map<string, Set<string>>,
): Part[] {
  const idx = parts.findIndex((p) => p.id === snapshot.id);
  if (idx < 0) {
    return [...parts, snapshot];
  }
  const owned = ownedFields.get(snapshot.id);
  if (!owned || owned.size === 0) {
    const next = [...parts];
    next[idx] = snapshot;
    return next;
  }
  const existingData = decode(parts[idx]);
  let patched = decode(snapshot);
  for (const field of owned) {
    const localValue = getFieldByPath(existingData, field);
    const snapshotValue = getFieldByPath(patched, field);
    if (typeof localValue !== 'string') continue;
    // Append-only contract: take whichever side is longer. If the
    // snapshot has no string for this field (or a shorter one), keep
    // the local value.
    if (typeof snapshotValue !== 'string' || localValue.length >= snapshotValue.length) {
      patched = setFieldByPath(patched, field, localValue);
    }
  }
  const next = [...parts];
  next[idx] = encode(snapshot, patched);
  return next;
}

/**
 * Apply a streaming delta to the parts list. The delta-owned set is
 * updated as a side effect on the passed-in map (which the caller
 * has already cloned for immutability).
 */
function applyDelta(
  parts: Part[],
  args: {
    partId: string;
    messageId: string;
    sessionId: string;
    field: string;
    delta: string;
  },
  ownedFields: Map<string, Set<string>>,
): Part[] {
  // Mark ownership.
  let fields = ownedFields.get(args.partId);
  if (!fields) {
    fields = new Set();
    ownedFields.set(args.partId, fields);
  }
  fields.add(args.field);

  const idx = parts.findIndex((p) => p.id === args.partId);
  if (idx < 0) {
    // No part yet — synthesise a stub of `type: 'text'` and apply the delta.
    const data = setFieldByPath({ type: 'text' }, args.field, args.delta);
    const stub: Part = {
      id: args.partId,
      messageId: args.messageId,
      sessionId: args.sessionId,
      data: data as unknown as string,
    };
    return [...parts, stub];
  }
  const existing = decode(parts[idx]);
  const current = getFieldByPath(existing, args.field);
  const currentStr = typeof current === 'string' ? current : '';
  const updated = setFieldByPath(existing, args.field, currentStr + args.delta);
  const next = [...parts];
  next[idx] = encode(parts[idx], updated);
  return next;
}

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
  if (s === 'waiting' || s === 'busy' || s === 'done' || s === 'error' || s === 'interrupted') return s;
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
function reconcileLoad(state: SessionView, incoming: SessionView): SessionView {
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
  const nextSession = incoming.session ?? state.session;

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
        return reconcileLoad(state, action.view);
      }
      // Wholesale replacement. Preserve sessionId from the new view
      // so a load against a different session can't accidentally
      // mix state. Reset all reducer-internal bookkeeping — the
      // server state is now authoritative.
      return {
        ...action.view,
        _deltaOwnedFields: action.view._deltaOwnedFields,
        _refetchRequested: false,
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

/**
 * Dispatch table for SSE events. Lives in a helper so the main
 * reducer keeps the action-shape switch flat and readable.
 */
function reduceSseEvent(state: SessionView, event: SseEvent): SessionView {
  const props = event.properties || {};
  switch (event.type) {
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
  ) {
    return state;
  }
  return {
    ...state,
    messages: nextMessages,
    parts: nextParts,
    session: nextSession,
    pendingQuestion: nextPendingQuestion,
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
  };
}

function reduceSessionStatus(state: SessionView, props: Record<string, unknown>): SessionView {
  const status = normaliseStatus(props.status);
  if (!status) return state;
  // Treat `session.status` with status === idle the same as
  // session.idle — also flag a refetch so the host hook reconciles
  // any missed content.
  const refetch = isSessionStatusIdle({ properties: props });
  if (state.session && state.session.status === status && !refetch) return state;
  return {
    ...state,
    session: state.session ? { ...state.session, status } : state.session,
    _refetchRequested: refetch || state._refetchRequested,
  };
}

function reduceSessionIdle(state: SessionView): SessionView {
  let nextSession = state.session;
  if (state.session && state.session.status !== 'done') {
    nextSession = { ...state.session, status: 'done' };
  }
  return {
    ...state,
    session: nextSession,
    _refetchRequested: true,
  };
}

function reducePermissionAsked(state: SessionView, event: SseEvent): SessionView {
  const perm = extractPendingPermission({
    type: event.type,
    properties: event.properties,
  });
  if (!perm) return state;
  if (state.pendingPermission && state.pendingPermission.permissionId === perm.permissionId) {
    return state;
  }
  // New permission: clear any stale judge state from the previous prompt.
  return {
    ...state,
    pendingPermission: perm,
    judgeStartsAt: null,
    judgeReasoning: null,
  };
}

function reducePermissionReplied(state: SessionView, props: Record<string, unknown>): SessionView {
  if (!state.pendingPermission) return state;
  const repliedId =
    (typeof props.requestID === 'string' && props.requestID) ||
    (typeof props.requestId === 'string' && props.requestId) ||
    (typeof props.id === 'string' && props.id) ||
    (typeof props.permissionID === 'string' && props.permissionID) ||
    '';
  if (!repliedId || state.pendingPermission.permissionId !== repliedId) return state;
  return {
    ...state,
    pendingPermission: null,
    judgeStartsAt: null,
    checkingPermissionId: state.checkingPermissionId === repliedId ? null : state.checkingPermissionId,
    judgeReasoning: null,
  };
}

/**
 * Fired by the backend immediately after a permission.asked event when
 * auto-approve is enabled. Carries `judgeStartsAt` (Unix-ms) so the UI
 * can show a live countdown without any local timer state.
 */
function reducePermissionPending(state: SessionView, props: Record<string, unknown>): SessionView {
  const permId = typeof props.permissionId === 'string' ? props.permissionId : null;
  const judgeStartsAt = typeof props.judgeStartsAt === 'number' ? props.judgeStartsAt : null;
  if (!permId || judgeStartsAt === null) return state;
  // Accept even if pendingPermission isn't set yet — the event may arrive
  // before or after permission.asked depending on goroutine scheduling.
  // Only apply if the permission IDs match when pendingPermission is set.
  if (state.pendingPermission !== null && state.pendingPermission.permissionId !== permId) return state;
  if (state.judgeStartsAt === judgeStartsAt) return state;
  return { ...state, judgeStartsAt };
}

/**
 * Fired by the backend when the LLM judge has started evaluating a
 * permission. Sets `checkingPermissionId` so the UI can show a spinner.
 */
function reducePermissionChecking(state: SessionView, props: Record<string, unknown>): SessionView {
  const permId = typeof props.permissionId === 'string' ? props.permissionId : null;
  if (!permId) return state;
  if (state.checkingPermissionId === permId) return state;
  // Countdown is over — clear judgeStartsAt and set checking.
  return { ...state, judgeStartsAt: null, checkingPermissionId: permId };
}

/**
 * Fired by the backend when the LLM judge returned an unsafe verdict and
 * handed the permission back to the human. Stores the one-line reasoning
 * so the prompt can show *why* the AI flagged it.
 */
function reducePermissionFlagged(state: SessionView, props: Record<string, unknown>): SessionView {
  const permId = typeof props.permissionId === 'string' ? props.permissionId : null;
  const reasoning = typeof props.reasoning === 'string' && props.reasoning
    ? props.reasoning
    : null;
  // Need at least one of the two to be useful. The judge session ID
  // used to be required (and rendered as a magnifying-glass link), but
  // those sessions are now deleted post-verdict — reasoning is the only
  // surviving signal.
  if (!permId || !reasoning) return state;
  // Apply if the permission IDs match (or pendingPermission is null — the
  // event may arrive just as the user manually replied).
  if (state.pendingPermission !== null && state.pendingPermission.permissionId !== permId) return state;
  return {
    ...state,
    checkingPermissionId: state.checkingPermissionId === permId ? null : state.checkingPermissionId,
    judgeReasoning: reasoning,
  };
}

/**
 * Fired by the backend when the LLM judge approved a permission.
 * Injects the approval notice into the thread and clears the pending
 * permission prompt and checking indicator.
 */
function reducePermissionAutoApproved(state: SessionView, props: Record<string, unknown>): SessionView {
  const permId = typeof props.permissionId === 'string' ? props.permissionId : '';
  const permission = typeof props.permission === 'string' ? props.permission : '';
  const patterns = Array.isArray(props.patterns)
    ? (props.patterns as unknown[]).filter((p): p is string => typeof p === 'string')
    : [];
  const reasoning = typeof props.reasoning === 'string' ? props.reasoning : '';
  const approvedBy = props.approvedBy === 'user' ? 'user' : 'ai';
  const approvedAt = typeof props.approvedAt === 'number' ? props.approvedAt : Date.now();

  if (!permId || !permission) return state;

  // Inject the approval notice. Stable key uses the permission ID so
  // repeated deliveries (SSE reconnect + initial GET re-injection)
  // dedupe deterministically.
  const stableKey = `ocman-notice-${permId}`;
  let messages = state.messages;
  let parts = state.parts;
  if (!messages.some((m) => m.id === stableKey)) {
    const noticeMsg: import('./api').Message = {
      id: stableKey,
      sessionId: state.sessionId,
      timeCreated: approvedAt,
      data: { role: 'notice' },
    };
    const noticePart: import('./api').Part = {
      id: `${stableKey}-part`,
      messageId: stableKey,
      sessionId: state.sessionId,
      timeCreated: approvedAt,
      data: JSON.stringify({
        type: 'auto-approved',
        permission,
        patterns,
        reasoning,
        approvedBy,
      }),
    };
    messages = upsertMessage(messages, noticeMsg);
    parts = [...parts, noticePart];
  }

  // Clear the pending permission if it matches.
  const pendingPermission =
    state.pendingPermission?.permissionId === permId ? null : state.pendingPermission;

  return {
    ...state,
    messages,
    parts,
    pendingPermission,
    judgeStartsAt: pendingPermission === null ? null : state.judgeStartsAt,
    checkingPermissionId: state.checkingPermissionId === permId ? null : state.checkingPermissionId,
    judgeReasoning: pendingPermission === null ? null : state.judgeReasoning,
  };
}

function reduceQuestionAsked(state: SessionView, event: SseEvent): SessionView {
  const q = extractPendingQuestion({
    type: event.type,
    properties: event.properties,
  });
  if (!q) return state;
  if (state.pendingQuestion && state.pendingQuestion.requestId === q.requestId) return state;
  return { ...state, pendingQuestion: q };
}

function reduceQuestionCleared(state: SessionView, props: Record<string, unknown>): SessionView {
  if (!state.pendingQuestion) return state;
  const repliedId =
    (typeof props.requestID === 'string' && props.requestID) ||
    (typeof props.requestId === 'string' && props.requestId) ||
    (typeof props.id === 'string' && props.id) ||
    '';
  if (!repliedId || state.pendingQuestion.requestId !== repliedId) return state;
  return { ...state, pendingQuestion: null };
}
