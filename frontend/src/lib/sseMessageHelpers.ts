import type { Message, Part, Session } from './api';

/**
 * Maximum length for part text/output strings before they are truncated
 * in the frontend cache. Mirrors the backend's `maxOutputLen`.
 */
export const MAX_OUTPUT_LEN = 200_000;

/**
 * Truncate large string fields on a Part to keep memory usage bounded.
 * Non-string values are returned unchanged. The truncation marker is the
 * same one the backend uses, so the UI doesn't need to special-case
 * frontend- vs backend-truncated payloads.
 */
export function truncatePartField(value: unknown): unknown {
  if (typeof value === 'string' && value.length > MAX_OUTPUT_LEN) {
    return value.slice(0, MAX_OUTPUT_LEN) + '\n... (truncated)';
  }
  return value;
}

/**
 * Insert a message into a chronologically sorted array, replacing any
 * existing entry with the same id and dropping placeholder/error stubs
 * that the real message supersedes (`temp-*`, `error-*`).
 *
 * The result is always sorted ascending by `timeCreated`; ties keep
 * insertion order.
 */
export function insertMessageByTime(prev: Message[], newMsg: Message): Message[] {
  const filtered = prev.filter(
    (m) => m.id !== newMsg.id && !m.id.startsWith('temp-') && !m.id.startsWith('error-'),
  );
  const idx = filtered.findIndex((m) => m.timeCreated > newMsg.timeCreated);
  if (idx === -1) return [...filtered, newMsg];
  return [...filtered.slice(0, idx), newMsg, ...filtered.slice(idx)];
}

/**
 * Merge a batch of new parts into an existing array. Existing parts with
 * matching ids are replaced (the new ones win) and stale optimistic /
 * error placeholders (`part-temp-*`, `part-error-*`) are dropped.
 */
export function mergeParts(prev: Part[], newParts: Part[]): Part[] {
  if (newParts.length === 0) return prev;
  const newIds = new Set(newParts.map((p) => p.id));
  const filtered = prev.filter(
    (p) => !newIds.has(p.id) && !p.id.startsWith('part-temp-') && !p.id.startsWith('part-error-'),
  );
  return [...filtered, ...newParts];
}

/**
 * Update a part in place when its id is already present, otherwise
 * append it to the end. Used by SSE handlers that only carry a single
 * part update at a time.
 */
export function upsertPart(prev: Part[], part: Part): Part[] {
  const idx = prev.findIndex((p) => p.id === part.id);
  if (idx >= 0) {
    const updated = [...prev];
    updated[idx] = part;
    return updated;
  }
  return [...prev, part];
}

/**
 * Like `mergeParts`, but routes each incoming part through the
 * length-non-decreasing merge so accumulating string fields (`text`,
 * `state.output`) are never shortened.
 *
 * Used for the `parts` array embedded in `message.created` and
 * `message.updated` events. That snapshot lags the live
 * `message.part.delta` stream for streaming text parts, but it is
 * the **only** channel that delivers most tool parts (status
 * transitions, embedded `state.input`, question prompts), so we
 * cannot ignore it without losing tool blocks until the next manual
 * refresh.
 */
export function mergePartsNonClobbering(prev: Part[], newParts: Part[]): Part[] {
  if (newParts.length === 0) return prev;
  // Drop stale optimistic / error placeholders so the embedded
  // snapshot still clears `part-temp-*` / `part-error-*` stubs the
  // way `mergeParts` does. Parts that share an id with one of
  // `newParts` are kept; `upsertPartNonClobbering` will merge them
  // below and pick the longer accumulating fields.
  let next = prev.filter(
    (p) => !p.id.startsWith('part-temp-') && !p.id.startsWith('part-error-'),
  );
  for (const part of newParts) {
    next = upsertPartNonClobbering(next, part);
  }
  return next;
}

/**
 * Like `upsertPart`, but never shortens accumulating string fields.
 *
 * `message.part.updated` snapshots and `message.part.delta` deltas
 * arrive on independent channels; either can be a few tokens ahead
 * of the other for the same part. A plain upsert from the snapshot
 * would overwrite locally accumulated `text` (or `state.output`)
 * with whatever the server had committed when it serialised the
 * snapshot, producing the "stream pauses and jumps backward"
 * symptom.
 *
 * For the two fields where we know strings only grow during
 * streaming, we keep the longer of (incoming, existing). All other
 * fields are taken from the incoming snapshot — that's what carries
 * fresh metadata like `state.status`, `time.end`, tool names, etc.
 */
export function upsertPartNonClobbering(prev: Part[], part: Part): Part[] {
  const idx = prev.findIndex((p) => p.id === part.id);
  if (idx < 0) return [...prev, part];

  const existing = prev[idx];
  const merged = mergeAccumulatingFields(existing, part);
  if (merged === part) {
    // Snapshot is at least as long as the local copy on every
    // tracked field — accept it wholesale.
    const updated = [...prev];
    updated[idx] = part;
    return updated;
  }
  const updated = [...prev];
  updated[idx] = merged;
  return updated;
}

/**
 * Compare the two parts' accumulating string fields and return a
 * merged Part. When every tracked field on the incoming snapshot is
 * at least as long as the existing one, returns `incoming` as-is so
 * the caller can fast-path. Otherwise returns a new Part with the
 * incoming snapshot's metadata plus the longer string for each
 * tracked field.
 */
function mergeAccumulatingFields(existing: Part, incoming: Part): Part {
  const existingData = decodePartData(existing);
  const incomingData = decodePartData(incoming);
  if (!existingData || !incomingData) return incoming;

  const existingText = typeof existingData.text === 'string' ? existingData.text : '';
  const incomingText = typeof incomingData.text === 'string' ? incomingData.text : '';

  const existingState = (existingData.state && typeof existingData.state === 'object')
    ? existingData.state as Record<string, unknown>
    : null;
  const incomingState = (incomingData.state && typeof incomingData.state === 'object')
    ? incomingData.state as Record<string, unknown>
    : null;
  const existingOutput = typeof existingState?.output === 'string' ? (existingState.output as string) : '';
  const incomingOutput = typeof incomingState?.output === 'string' ? (incomingState.output as string) : '';

  const textShorter = incomingText.length < existingText.length;
  const outputShorter = incomingOutput.length < existingOutput.length;
  if (!textShorter && !outputShorter) return incoming;

  // Build a new data object: take the incoming snapshot wholesale,
  // then patch the two accumulating fields back to the longer value.
  const mergedData: Record<string, unknown> = { ...incomingData };
  if (textShorter) mergedData.text = existingText;
  if (outputShorter && incomingState) {
    mergedData.state = { ...incomingState, output: existingOutput };
  } else if (outputShorter && existingState) {
    // Snapshot dropped `state` entirely — preserve the existing one.
    mergedData.state = existingState;
  }

  return {
    id: incoming.id,
    messageId: incoming.messageId,
    sessionId: incoming.sessionId,
    data: mergedData as unknown as string,
  };
}

function decodePartData(part: Part): Record<string, unknown> | null {
  const data = part.data as unknown;
  if (typeof data === 'string') {
    try {
      return JSON.parse(data) as Record<string, unknown>;
    } catch {
      return null;
    }
  }
  if (data && typeof data === 'object') return data as Record<string, unknown>;
  return null;
}

/**
 * Derive a session status from a single (typically the latest) message.
 *
 * - User messages keep the session in `done`.
 * - Assistant messages with an explicit error (`finish === 'error'` or
 *   `data.error` set) report `error`.
 * - Assistant messages with any other finish reason report `waiting`
 *   (model finished — backend may still be wrapping up).
 * - Assistant messages without a finish reason report `busy`.
 */
export function inferStatusFromMessage(msg: Message): Session['status'] {
  if (msg.data.role !== 'assistant') return 'done';
  if (msg.data.finish === 'error' || msg.data.error) return 'error';
  if (msg.data.finish) return 'waiting';
  return 'busy';
}
