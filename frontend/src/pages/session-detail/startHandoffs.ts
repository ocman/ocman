import { useEffect } from 'react';
import type { StartSteps } from './StartProgress';

export interface StartHandoff { prompt: string; steps: StartSteps }

// A delivered first prompt, keyed by the new session id, so the session view
// keeps showing it instead of an empty thread until the first message lands.
// A failed delivery is never handed off: failed-send recovery owns that prompt.
export const startHandoffs = new Map<string, StartHandoff>();

/** The session's handoff while it has no messages; the first message ends it. */
export function useStartHandoff(sessionId: string | undefined, messageCount: number): StartHandoff | undefined {
  const done = messageCount > 0;
  useEffect(() => {
    if (sessionId && done) startHandoffs.delete(sessionId);
  }, [sessionId, done]);
  return sessionId && !done ? startHandoffs.get(sessionId) : undefined;
}
