import type { Message, Part, Session } from './api';

/**
 * A compact fingerprint of the session fields that affect rendering and
 * header state. Used by `SessionDetail` to skip redundant state updates when
 * a background refetch returns unchanged data.
 *
 * Keep the set of hashed fields in sync with the fields read by the session
 * header — adding a new header field that isn't hashed here will cause it to
 * become stale on identical-looking refetches.
 */
export function hashSession(
  s: Session & {
    contextTokenCount?: number;
    defaultAgent?: string;
    defaultModel?: string;
  },
): string {
  return JSON.stringify({
    id: s.id,
    status: s.status,
    title: s.title,
    ctx: s.contextTokenCount,
    agent: s.defaultAgent,
    model: s.defaultModel,
    notice: s.notice ?? null,
  });
}

/**
 * A compact fingerprint of the message list and its parts, used to detect
 * content changes between loads. Includes message ids + creation times and
 * part ids + data so tool-call status updates and streamed text are caught.
 */
export function hashMessagesAndParts(messages: Message[], parts: Part[]): string {
  return (
    messages.map((m) => m.id + ':' + m.timeCreated).join(',') +
    '|' +
    parts.map((p) => p.id + ':' + JSON.stringify(p.data)).join(',')
  );
}
