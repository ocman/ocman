import type { Message, Part } from './api';
import type { PendingQuestion, QuestionItem } from '../components/session/QuestionPrompt';
import { truncatePartField } from './sseMessageHelpers';

/**
 * Permission prompt awaiting a user reply. Mirrors the
 * `permission.asked` SSE event payload.
 *
 * `sessionId` is the (sub)agent that asked for the permission. When
 * the prompt comes from a subagent of the page's session, the
 * response must be POSTed to that subagent's session, not the
 * parent's. Empty string means "asked by the page's own session"
 * (legacy payloads without a sessionID).
 */
export interface PendingPermission {
  permissionId: string;
  permission: string;
  patterns: string[];
  metadata?: Record<string, unknown>;
  sessionId: string;
  /** Unix-ms timestamp of when this permission prompt was first received.
   *  Used to anchor the auto-approve countdown so it survives session
   *  switches (the component remounts but the remaining time is correct). */
  askedAt: number;
}

/** Tool names that the question prompt can be carried under. */
export const QUESTION_TOOL_NAMES: readonly string[] = [
  'question',
  'mcp_question',
  'Question',
  'mcp_Question',
];

/**
 * Try to extract a Message + Parts from a raw SSE event payload.
 * OpenCode SSE events for message changes carry an `info` object (the
 * message metadata) and an optional `parts` array — the same
 * structure the backend's `convertOpenCodeMessages` reads from the
 * HTTP API.
 *
 * Returns `null` when the event doesn't contain usable message data.
 */
export function extractMessageFromEvent(
  parsed: Record<string, unknown>,
  sessionId: string,
): { message: Message; parts: Part[] } | null {
  // The event can wrap the payload in `properties` or carry it at the
  // top level.
  const props = (parsed.properties && typeof parsed.properties === 'object')
    ? parsed.properties as Record<string, unknown>
    : parsed;

  const info = props.info as Record<string, unknown> | undefined;
  if (!info || typeof info !== 'object') return null;

  const msgId = info.id as string | undefined;
  if (!msgId) return null;

  const timeObj = info.time as Record<string, unknown> | undefined;
  const timeCreated = typeof timeObj?.created === 'number' ? timeObj.created : Date.now();

  // Default to 'assistant' if role is not yet set (can happen during
  // early streaming).
  const role = (info.role as string) || 'assistant';

  const message: Message = {
    id: msgId,
    sessionId: (info.sessionID as string) || sessionId,
    timeCreated,
    data: {
      role,
      finish: info.finish as string | undefined,
      modelID: info.modelID as string | undefined,
      providerID: info.providerID as string | undefined,
      agent: info.agent as string | undefined,
      mode: info.mode as string | undefined,
      cost: typeof info.cost === 'number' ? info.cost : undefined,
      tokens: info.tokens as Message['data']['tokens'],
      time: info.time as Message['data']['time'],
      error: info.error as Message['data']['error'],
    },
  };

  const parts: Part[] = [];
  const rawParts = props.parts;
  if (Array.isArray(rawParts)) {
    for (const p of rawParts) {
      if (!p || typeof p !== 'object') continue;
      const part = p as Record<string, unknown>;
      const partType = part.type as string | undefined;
      // Skip non-essential types (same filter as backend
      // convertOpenCodeMessages).
      if (partType === 'step-start' || partType === 'step-finish' || partType === 'snapshot') continue;

      // Truncate large outputs.
      if (typeof part.text === 'string') part.text = truncatePartField(part.text);
      const state = part.state as Record<string, unknown> | undefined;
      if (state) {
        if (typeof state.output === 'string') state.output = truncatePartField(state.output);
        const meta = state.metadata as Record<string, unknown> | undefined;
        if (meta && typeof meta.output === 'string') meta.output = truncatePartField(meta.output);
      }

      parts.push({
        id: (part.id as string) || `sse-part-${msgId}-${parts.length}`,
        messageId: (part.messageID as string) || msgId,
        sessionId: (part.sessionID as string) || sessionId,
        data: part as unknown as string, // stored as the raw object, same as backend
      });
    }
  }

  return { message, parts };
}

/**
 * Try to extract a single Part update from an SSE event.
 * `part.updated` events carry the part data directly in `properties`.
 * Returns `null` for events that lack the required fields or that
 * carry a lifecycle-only part type.
 */
export function extractPartFromEvent(
  parsed: Record<string, unknown>,
  sessionId: string,
): Part | null {
  const props = (parsed.properties && typeof parsed.properties === 'object')
    ? parsed.properties as Record<string, unknown>
    : parsed;

  const partId = props.id as string | undefined;
  const messageId = props.messageID as string | undefined;
  if (!partId || !messageId) return null;

  const partType = props.type as string | undefined;
  if (partType === 'step-start' || partType === 'step-finish' || partType === 'snapshot') return null;

  // Truncate large fields.
  if (typeof props.text === 'string') props.text = truncatePartField(props.text);
  const state = props.state as Record<string, unknown> | undefined;
  if (state) {
    if (typeof state.output === 'string') state.output = truncatePartField(state.output);
    const meta = state.metadata as Record<string, unknown> | undefined;
    if (meta && typeof meta.output === 'string') meta.output = truncatePartField(meta.output);
  }

  return {
    id: partId,
    messageId,
    sessionId: (props.sessionID as string) || sessionId,
    data: props as unknown as string,
  };
}

/**
 * Checks whether a parsed `session.status` SSE event reports the
 * session as idle (the only terminal state among busy / retry /
 * idle). Used to avoid clearing pending permission/question prompts
 * during intermediate status transitions fired mid-request.
 */
export function isSessionStatusIdle(parsed: Record<string, unknown>): boolean {
  const props = parsed.properties as Record<string, unknown> | undefined;
  if (!props) return false;
  const status = props.status;
  if (typeof status === 'string') return status === 'idle';
  if (status && typeof status === 'object' && !Array.isArray(status)) {
    const t = (status as Record<string, unknown>).type;
    return typeof t === 'string' && t === 'idle';
  }
  return false;
}

// First key in `keys` whose value on `obj` is a non-empty string,
// else ''. Centralizes the defensive `typeof x === 'string' && x`
// checks used when parsing untrusted SSE payloads.
function pickString(obj: Record<string, unknown>, ...keys: string[]): string {
  for (const k of keys) {
    const v = obj[k];
    if (typeof v === 'string' && v) return v;
  }
  return '';
}

/**
 * Extract a `PendingPermission` from a `permission.asked` SSE event.
 * Returns `null` when the event isn't a permission ask or lacks a
 * usable id.
 */
export function extractPendingPermission(node: unknown): PendingPermission | null {
  if (!node || typeof node !== 'object' || Array.isArray(node)) return null;
  const obj = node as Record<string, unknown>;

  if (obj.type !== 'permission.asked') return null;

  const properties = (obj.properties && typeof obj.properties === 'object')
    ? obj.properties as Record<string, unknown>
    : {};

  const id = pickString(properties, 'id', 'requestID');
  if (!id) return null;

  const permission = pickString(properties, 'permission') || 'Permission required';

  const rawPatterns = properties.patterns;
  const patterns = Array.isArray(rawPatterns)
    ? rawPatterns.filter((p): p is string => typeof p === 'string')
    : [];

  const metadata = properties.metadata && typeof properties.metadata === 'object' && !Array.isArray(properties.metadata)
    ? properties.metadata as Record<string, unknown>
    : undefined;

  // sessionID is the (sub)agent that issued the prompt.
  const sessionId = pickString(properties, 'sessionID', 'sessionId');

  return {
    permissionId: id,
    permission,
    patterns,
    ...(metadata ? { metadata } : {}),
    sessionId,
    askedAt: Date.now(),
  };
}

/**
 * Extract a `PendingQuestion` from a `question.asked` SSE event.
 * Returns `null` when the event isn't a question ask, lacks an id,
 * or carries no parsable questions.
 */
export function extractPendingQuestion(node: unknown): PendingQuestion | null {
  if (!node || typeof node !== 'object' || Array.isArray(node)) return null;
  const obj = node as Record<string, unknown>;

  if (obj.type !== 'question.asked') return null;

  const properties = (obj.properties && typeof obj.properties === 'object')
    ? obj.properties as Record<string, unknown>
    : {};

  const id = pickString(properties, 'id', 'requestID', 'requestId');
  if (!id) return null;

  const sessionID = pickString(properties, 'sessionID', 'sessionId');

  const rawQuestions = properties.questions;
  const questions = normalizeQuestionItems(rawQuestions);
  if (questions.length === 0) return null;

  return { requestId: id, sessionID, questions };
}

/**
 * Coerce a question payload (string / object / array) into a flat
 * `QuestionItem[]`. Strings are JSON-parsed first; wrapper objects
 * with a `questions` field are unwrapped; entries lacking a string
 * `question` or array `options` are dropped.
 */
export function normalizeQuestionItems(raw: unknown): QuestionItem[] {
  let value = raw;

  if (typeof value === 'string') {
    try {
      value = JSON.parse(value) as unknown;
    } catch {
      return [];
    }
  }

  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const obj = value as Record<string, unknown>;
    if (Array.isArray(obj.questions)) value = obj.questions;
  }

  if (!Array.isArray(value) || value.length === 0) return [];

  return value.filter(
    (q): q is QuestionItem =>
      !!q &&
      typeof q === 'object' &&
      typeof (q as QuestionItem).question === 'string' &&
      Array.isArray((q as QuestionItem).options),
  );
}

/**
 * Returns true when a question tool's `output` indicates the user
 * has actually answered. Empty / `""` / `[]` are all treated as
 * "still pending".
 */
export function hasQuestionOutput(output: unknown): boolean {
  return output != null && output !== '' && output !== '""' && output !== '[]';
}

/**
 * Extract a `PendingQuestion` from a single tool part. Returns
 * `null` unless the part is an unanswered `question` tool call with
 * a parsable id and questions array.
 */
export function extractPendingQuestionFromPart(part: Part, sessionId: string): PendingQuestion | null {
  let pd: Record<string, unknown>;
  try {
    pd = typeof part.data === 'string'
      ? JSON.parse(part.data)
      : (part.data as unknown as Record<string, unknown>);
  } catch {
    return null;
  }

  if (pd.type !== 'tool') return null;
  const toolName = pd.tool as string | undefined;
  if (!toolName || !QUESTION_TOOL_NAMES.includes(toolName)) return null;

  const state = pd.state as Record<string, unknown> | undefined;
  if (!state) return null;

  const status = state.status as string | undefined;
  if (status !== 'running' && hasQuestionOutput(state.output)) return null;

  const input = (state.input && typeof state.input === 'object' && !Array.isArray(state.input))
    ? state.input as Record<string, unknown>
    : {};
  const metadata = (state.metadata && typeof state.metadata === 'object' && !Array.isArray(state.metadata))
    ? state.metadata as Record<string, unknown>
    : {};

  const requestId =
    (typeof input.requestId === 'string' && input.requestId) ||
    (typeof input.requestID === 'string' && input.requestID) ||
    (typeof input.id === 'string' && input.id) ||
    (typeof metadata.requestId === 'string' && metadata.requestId) ||
    (typeof metadata.requestID === 'string' && metadata.requestID) ||
    (typeof metadata.id === 'string' && metadata.id) ||
    '';
  if (!requestId) return null;

  const questions = normalizeQuestionItems(input.questions ?? state.input);
  if (questions.length === 0) return null;

  const pendingSessionId =
    (typeof input.sessionID === 'string' && input.sessionID) ||
    (typeof input.sessionId === 'string' && input.sessionId) ||
    sessionId;

  return { requestId, sessionID: pendingSessionId, questions };
}

/**
 * Walk a parts list newest-first and return the first
 * `PendingQuestion` found, or `null` if none exists.
 */
export function extractPendingQuestionFromParts(parts: Part[], sessionId: string): PendingQuestion | null {
  for (let i = parts.length - 1; i >= 0; i--) {
    const pending = extractPendingQuestionFromPart(parts[i], sessionId);
    if (pending) return pending;
  }
  return null;
}

/**
 * If `part` is a question tool call that has now been *answered*
 * (status is no longer `running` and it carries real output), return
 * its requestId so the caller can clear a matching pending question.
 * Returns `null` for non-question parts and for questions still
 * awaiting an answer.
 *
 * This covers the case where the user answers a question in the
 * OpenCode CLI (or any other client): OpenCode fills in the tool
 * part's output and streams a `message.part.updated`, but does not
 * always emit a `question.replied` / `question.rejected` event. The
 * reducer uses this to dismiss ocman's own prompt when the backing
 * tool part resolves.
 */
export function answeredQuestionRequestId(part: Part): string | null {
  let pd: Record<string, unknown>;
  try {
    pd = typeof part.data === 'string'
      ? JSON.parse(part.data)
      : (part.data as unknown as Record<string, unknown>);
  } catch {
    return null;
  }

  if (pd.type !== 'tool') return null;
  const toolName = pd.tool as string | undefined;
  if (!toolName || !QUESTION_TOOL_NAMES.includes(toolName)) return null;

  const state = pd.state as Record<string, unknown> | undefined;
  if (!state) return null;

  const status = state.status as string | undefined;
  // Still waiting on the user — not answered yet.
  if (status === 'running' || status === 'pending') return null;
  if (!hasQuestionOutput(state.output)) return null;

  const input = (state.input && typeof state.input === 'object' && !Array.isArray(state.input))
    ? state.input as Record<string, unknown>
    : {};
  const metadata = (state.metadata && typeof state.metadata === 'object' && !Array.isArray(state.metadata))
    ? state.metadata as Record<string, unknown>
    : {};

  const requestId =
    (typeof input.requestId === 'string' && input.requestId) ||
    (typeof input.requestID === 'string' && input.requestID) ||
    (typeof input.id === 'string' && input.id) ||
    (typeof metadata.requestId === 'string' && metadata.requestId) ||
    (typeof metadata.requestID === 'string' && metadata.requestID) ||
    (typeof metadata.id === 'string' && metadata.id) ||
    '';

  return requestId || null;
}

/** Check if loaded parts contain a pending (unanswered) question tool call. */
export function hasPendingQuestionInParts(parts: Part[], sessionId: string): boolean {
  if (extractPendingQuestionFromParts(parts, sessionId)) return true;

  for (let i = parts.length - 1; i >= 0; i--) {
    let pd: Record<string, unknown>;
    try {
      const data = parts[i].data;
      pd = typeof data === 'string' ? JSON.parse(data) : (data as unknown as Record<string, unknown>);
    } catch {
      continue;
    }

    if (pd.type !== 'tool') continue;
    const toolName = pd.tool as string | undefined;
    if (!toolName || !QUESTION_TOOL_NAMES.includes(toolName)) continue;

    const state = pd.state as Record<string, unknown> | undefined;
    if (!state) continue;
    if (state.status === 'running' || !hasQuestionOutput(state.output)) return true;
  }

  return false;
}

/**
 * Truncate a raw SSE data string for debug-overlay display. Long
 * payloads are clipped at `max` characters with an ellipsis marker.
 */
export function truncateSseData(raw: string, max = 500): string {
  if (raw.length <= max) return raw;
  return raw.slice(0, max) + '...';
}
