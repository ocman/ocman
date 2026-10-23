// State and action types for the SessionDetail reducer (sessionReducer.ts).

import type { Message, Part, SessionDetail } from './api';
import type { PendingPermission } from './sseHelpers';
import type { PendingQuestion } from '../components/session/QuestionPrompt';

/** Session metadata as held by the page, including SessionDetail extras. */
export type SessionMetadata = SessionDetail['session'] & {
  defaultAgent?: string;
  defaultModel?: string;
  warnings?: SessionDetail['warnings'];
};

/**
 * The single piece of state the SessionDetail page owns.
 *
 * `_deltaOwnedFields` and `_refetchRequested` are reducer-internal
 * bookkeeping prefixed with `_` so consumers don't accidentally
 * depend on them. They survive stream batches but are cleared on
 * `load` (the freshly-fetched server state is authoritative).
 */
export interface SessionView {
  /** Active session id this view describes. */
  sessionId: string;
  /** Session metadata header (status, agent, timing, ...). `null`
   *  until the first `load` lands. */
  session: SessionMetadata | null;
  /** Live background work reported by the backend, never inferred from messages. */
  backgroundTaskCount?: number;
  backgroundTasks?: import('./sessionBackgroundTasks').BackgroundTask[];
  /** False for cached metadata until REST or a status-bearing live event confirms it. */
  _statusFresh?: boolean;
  /** Bumped by every live status observation. A refetch started at an older
   *  revision must not overwrite the live status. */
  _statusRevision?: number;
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
  /** True from a stream event that makes REST history stale (turn end,
   *  history rewrite, stream reset, unknown child) until the next load
   *  or recovery. Nothing refetches on it; the stream reconciles on its
   *  own. It only holds back speech until history is reconciled. */
  _refetchRequested: boolean;
  /**
   * Set by the stream's `prompt.judge` scheduled phase.
   * Unix-ms timestamp of when the LLM judge will start (after the
   * configured delay). The UI counts down to this time. Cleared when
   * the checking phase arrives (judge has started) or when
   * the permission is cleared.
   */
  judgeStartsAt: number | null;
  /**
   * Set by the stream's `prompt.judge` checking phase.
   * Contains the permission ID currently being evaluated by the
   * server-side LLM judge. Cleared when the prompt resolves.
   * The UI uses this to show a "checking" spinner on the prompt.
   */
  checkingPermissionId: string | null;
  /**
   * The judge's one-line conclusion (the "reasoning" field of the JSON
   * it emits), surfaced on the prompt so the user can see *why* the AI
   * flagged a permission. Set by the `prompt.judge` flagged phase and
   * cleared when `pendingPermission` is cleared.
   *
   * The transient OpenCode session that produced this reasoning is
   * deleted by the backend as soon as the verdict is parsed, so the
   * UI no longer offers a "view judge session" link — `judgeReasoning`
   * is the only judge artifact carried in client state.
   */
  judgeReasoning: string | null;
  /** Parts whose stream skipped ahead of local content; reconcile heals them. */
  _stale?: ReadonlySet<string>;
  /** Stream-owned ledger and current-turn content, separate from REST history. */
  _live?: SessionView;
  /** Authoritative replacements within the stream-only current-turn ledger. */
  _liveFinalParts?: ReadonlySet<string>;
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
  approvedBy?: 'user' | 'ai';
  reply?: 'once' | 'always';
  metadata?: Record<string, unknown>;
  askedAt?: number;
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
   *   triggered by stream signals such as a settled turn, where
   *   OpenCode's database may lag the live stream by a few hundred ms — a wholesale replace would
   *   transiently wipe parts that were just streamed.
   *
   *   `_deltaOwnedFields` is preserved in reconcile mode so live
   *   `state.output` deltas don't get overwritten by stale
   *   snapshots from the refetch.
   */
  | { type: 'load'; view: SessionView; mode?: 'replace' | 'reconcile'; statusRevision?: number }
  | { type: 'setPendingPermission'; permission: PendingPermission; ownerIds?: string[] }
  | { type: 'setPendingQuestion'; question: PendingQuestion }
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
    backgroundTaskCount: 0,
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
