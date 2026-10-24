import { listFailedSends, removeFailedSend } from '../lib/failedSends';

/** Test cleanup: drops every failed send recorded for a session. */
export function clearFailedSends(sessionId: string) {
  for (const { id } of listFailedSends(sessionId)) removeFailedSend(sessionId, id);
}
