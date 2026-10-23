import type { Part, PermissionRequest, QuestionRequest } from './api';
import type { PendingQuestion, QuestionItem } from '../components/session/QuestionPrompt';

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
  always?: string[];
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

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

/**
 * Read a pending permission from the REST list (`PermissionRequest`).
 * Fields beyond the id vary by platform, so each one is validated.
 * Returns `null` when the request lacks a usable id.
 */
export function permissionFromRequest(request: PermissionRequest): PendingPermission | null {
  const properties = asRecord(request);
  if (!properties) return null;

  const id = pickString(properties, 'id', 'requestID');
  if (!id) return null;

  const permission = pickString(properties, 'permission') || 'Permission required';

  const rawPatterns = properties.patterns;
  const patterns = Array.isArray(rawPatterns)
    ? rawPatterns.filter((p): p is string => typeof p === 'string')
    : [];

  const rawAlways = properties.always;
  const always = Array.isArray(rawAlways)
    ? rawAlways.filter((p): p is string => typeof p === 'string')
    : undefined;

  const metadata = asRecord(properties.metadata) ?? undefined;

  // sessionID is the (sub)agent that issued the prompt.
  const sessionId = pickString(properties, 'sessionID', 'sessionId');

  return {
    permissionId: id,
    permission,
    patterns,
    ...(always ? { always } : {}),
    ...(metadata ? { metadata } : {}),
    sessionId,
    askedAt: Date.now(),
  };
}

/**
 * Read a pending question from the REST list (`QuestionRequest`).
 * Returns `null` when the request lacks an id or carries no parsable
 * questions.
 */
export function questionFromRequest(request: QuestionRequest): PendingQuestion | null {
  const properties = asRecord(request);
  if (!properties) return null;

  const id = pickString(properties, 'id', 'requestID', 'requestId');
  if (!id) return null;

  const sessionID = pickString(properties, 'sessionID', 'sessionId');

  const questions = normalizeQuestionItems(properties.questions);
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
