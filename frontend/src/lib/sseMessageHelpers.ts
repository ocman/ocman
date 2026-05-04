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
