// Ocman conversation events (OCE), version 1. Mirrors internal/convevents;
// see spec/conversation-events/design.md. contracts.test.ts checks these
// types against the Go structs.

import type { Message, Part, PartData, QueuedMessage, Session, SessionNotice, SessionStatus } from '../api.types';
import type { QuestionItem } from '../../components/session/QuestionPrompt';
import type { BackgroundTask } from '../sessionBackgroundTasks';

export const OCE_VERSION = 1;

export type Durability = 'durable' | 'live' | 'ephemeral';

export interface OceEnvelope<E extends OceEvent = OceEvent> {
  v: 1;
  /** Stream epoch: owner process + session log generation. A change means "resume impossible". */
  epoch: string;
  /** Monotonic per (epoch, session). Holes are not gaps. */
  seq: number;
  /** The session this stream is about (the page session, never a child). */
  sessionId: string;
  /** Owner-qualified platform id, e.g. "opencode" or "r-<remote>:claudecode". */
  platform: string;
  /** Owner wall clock, Unix ms. Informational; ordering uses seq. */
  at: number;
  durability: Durability;
  /** Set on replays of lifecycle edges the owner already acted on. */
  replayed?: boolean;
  event: E;
}

/** Ocman's part schema; identical to REST Part except data is always an object. */
export interface OcePart extends Omit<Part, 'data'> {
  /** Source-defined ordering inside the message (statev2 Part.Position) when known. */
  position?: number;
  data: PartData;
}

export interface MessageUpserted { type: 'message.upserted'; message: Message; position?: number }
export interface MessageRemoved { type: 'message.removed'; messageId: string; reason: 'deleted' | 'reverted' | 'superseded' }
export interface PartUpserted {
  type: 'part.upserted';
  part: OcePart;
  /** True once the part can no longer grow. Final upserts replace local
   *  streaming fields unconditionally. */
  final: boolean;
}
export interface PartRemoved { type: 'part.removed'; messageId: string; partId: string }

export type DeltaField = 'text' | 'tool.input' | 'tool.output';
export interface PartDelta {
  type: 'part.delta';
  messageId: string;
  partId: string;
  field: DeltaField;
  /** Character (UTF-16 code unit) offset of `text` within the field. */
  offset: number;
  text: string;
  /** Part type hint so a client can create a correct stub ('text' | 'reasoning' | 'tool'). */
  partType?: string;
}

/** The PartData path each delta field appends to. */
export const DELTA_FIELD_PATH: Readonly<Record<DeltaField, string>> = {
  text: 'text',
  'tool.input': 'state.raw',
  'tool.output': 'state.output',
};

export interface SessionStatusEvent {
  type: 'session.status';
  status: SessionStatus;
  /** True when an idle edge was seen but the terminal kind is not settled yet. */
  provisional?: boolean;
  /** null clears any notice; undefined leaves it unchanged. */
  notice?: SessionNotice | null;
  /** Only with status 'limited'. */
  resetsAt?: number;
}
export interface TurnStarted { type: 'turn.started'; startedAt: number; messageId?: string }
export interface TurnEnded {
  type: 'turn.ended';
  outcome: 'done' | 'error' | 'interrupted';
  endedAt: number;
  latestMessageId?: string;
}

export interface OcePermissionRequest {
  permission: string;
  patterns: string[];
  always?: string[];
  metadata?: Record<string, unknown>;
}
export interface OceQuestionRequest { questions: QuestionItem[] }
export interface OcePrompt {
  id: string;
  kind: 'permission' | 'question';
  /** The session that asked (may be a descendant of envelope.sessionId). */
  sessionId: string;
  /** First observation, from the owner's state.db receipt. */
  askedAt: number;
  permission?: OcePermissionRequest;
  question?: OceQuestionRequest;
}
export interface PromptAsked { type: 'prompt.asked'; prompt: OcePrompt }
export interface PromptJudge {
  type: 'prompt.judge';
  promptId: string;
  phase: 'scheduled' | 'checking' | 'flagged';
  startsAt?: number;
  reasoning?: string;
}
export interface PromptResolved {
  type: 'prompt.resolved';
  promptId: string;
  kind: 'permission' | 'question';
  outcome: 'once' | 'always' | 'reject' | 'answered' | 'dismissed' | 'cancelled';
  by: 'user' | 'ai' | 'rule' | 'external';
}

export type OceChild = Pick<Session, 'id' | 'parentId' | 'title' | 'timeCreated' | 'timeUpdated'> & {
  agent?: string;
  /** The child is gone; drop it. */
  deleted?: boolean;
};
export type OceSessionPatch = Partial<Pick<Session, 'title' | 'summaryAdditions' | 'summaryDeletions' | 'summaryFiles' | 'shareUrl' | 'archived'>> & {
  defaultAgent?: string;
  defaultModel?: string;
  revert?: OceRevertMarker | null;
};
export interface OceRevertMarker { messageId: string; partId?: string }
export interface OceErrorInfo { name?: string; message: string; statusCode?: number }

export interface QueueChanged { type: 'queue.changed'; messages: QueuedMessage[] }
export interface BackgroundChanged { type: 'background.changed'; count: number; tasks: BackgroundTask[] }
export interface ChildUpserted { type: 'child.upserted'; child: OceChild }
export interface ChildStatus { type: 'child.status'; childId: string; status: SessionStatus; notice?: SessionNotice | null }
export interface SessionUpdated { type: 'session.updated'; patch: OceSessionPatch }
export interface HistoryRewritten {
  type: 'history.rewritten';
  reason: 'compaction' | 'revert' | 'fork' | 'resync';
  /** Oldest message that may have changed; absent means the whole session. */
  fromMessageId?: string;
}
export interface HistoryCommitted { type: 'history.committed'; revision: number }
export interface ChangesDirty { type: 'changes.dirty'; reason: 'edit' | 'diff' | 'commit' }
export interface SessionError { type: 'session.error'; messageId?: string; error: OceErrorInfo }

export interface OceInflight { messages: Message[]; parts: OcePart[] }
export interface LiveOverlay {
  status: SessionStatusEvent;
  prompts: OcePrompt[];
  judge: PromptJudge[];
  background: BackgroundChanged;
  /** Present when the owner can read it cheaply. */
  queue?: QueueChanged;
  /** Messages and parts of the in-flight turn only, not history. */
  inflight: OceInflight;
  liveConnection: boolean;
}
export interface StreamHello {
  type: 'stream.hello';
  protocol: 1;
  /** True when `after` was honoured and replay follows; false means reconcile. */
  resumed: boolean;
  /** Present when !resumed (and always on a cold connect). */
  overlay?: LiveOverlay;
  /** Latest statev2 revision known to the owner for this session, 0 if none. */
  revision: number;
}
export interface StreamReset { type: 'stream.reset'; reason: 'epoch' | 'evicted' | 'slow-consumer' | 'source-restarted' }
export interface StreamError {
  type: 'stream.error';
  code: 'platform-unreachable' | 'owner-offline' | 'auth' | 'internal';
  message: string;
  retryable: boolean;
}

/** OCE capability flags (design §4.8); not yet part of PlatformCapabilities. */
export interface OceStreamCapabilities {
  streamResume: boolean;
  deltaFields: DeltaField[];
}

export type OceEvent =
  | StreamHello | StreamReset | StreamError
  | MessageUpserted | MessageRemoved | PartUpserted | PartRemoved | PartDelta
  | SessionStatusEvent | TurnStarted | TurnEnded | SessionUpdated
  | HistoryRewritten | HistoryCommitted
  | PromptAsked | PromptJudge | PromptResolved
  | QueueChanged | BackgroundChanged | ChildUpserted | ChildStatus | ChangesDirty | SessionError;

export type OceEventType = OceEvent['type'];

/** The resume cursor "<epoch>:<seq>". */
export function formatCursor(env: Pick<OceEnvelope, 'epoch' | 'seq'>): string {
  return `${env.epoch}:${env.seq}`;
}

/** Split a cursor; the epoch may itself contain colons. */
export function parseCursor(cursor: string): { epoch: string; seq: number } | null {
  const i = cursor.lastIndexOf(':');
  if (i <= 0) return null;
  const tail = cursor.slice(i + 1);
  if (!/^\d+$/.test(tail)) return null;
  const seq = Number(tail);
  if (!Number.isSafeInteger(seq)) return null;
  return { epoch: cursor.slice(0, i), seq };
}
