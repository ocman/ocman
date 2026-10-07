import { useEffect } from 'react';
import type { Message, Part } from '../../lib/api';
import { isImageMime, parsePart } from '../../lib/convertMessages';
import type { StartSteps } from './StartProgress';

export interface StartHandoff { prompt: string; steps: StartSteps }

// A delivered first prompt, keyed by the new session id, so the session view
// keeps showing it instead of an empty thread until the first message lands.
// A failed delivery is never handed off: failed-send recovery owns that prompt.
export const startHandoffs = new Map<string, StartHandoff>();

// The first submission's model survives navigation even before message metadata
// arrives. The composer consumes it separately from the visible prompt handoff.
export const startModels = new Map<string, string>();

/**
 * Keep the handoff until the user prompt has visible content, not just a header.
 * `viewSessionId` owns the messages: right after a route change the view
 * still holds the previous session's messages, which must not end it.
 */
export function useStartHandoff(sessionId: string | undefined, viewSessionId: string | undefined, messages: Message[], parts: Part[]): StartHandoff | undefined {
  const handoff = sessionId ? startHandoffs.get(sessionId) : undefined;
  const done = !!handoff && viewSessionId === sessionId && messages.some((message) => message.data?.role === 'user'
    && parts.some((part) => {
      if (part.messageId !== message.id) return false;
      const data = parsePart(part);
      return (data.type === 'text' && typeof data.text === 'string' && !!data.text.trim())
        || (data.type === 'file' && !!data.url && (isImageMime(data.mime) || !!data.filename));
    }));
  useEffect(() => {
    if (sessionId && done) startHandoffs.delete(sessionId);
  }, [sessionId, done]);
  return done ? undefined : handoff;
}
