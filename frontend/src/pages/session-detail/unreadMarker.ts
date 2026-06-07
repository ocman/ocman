// Helpers that compute the "first unread" marker and the unread
// message count shown above the composer when the user re-opens a
// session that has received new agent messages since they last
// viewed it. Kept as pure functions so they can be unit-tested
// without spinning up the SessionDetail React tree.
//
// Terminology: the "cutoff" is the session.seenTimeUpdated snapshot
// captured at first mount — the user's last-seen timeUpdated for
// this session. A message is unread when its timeCreated is
// strictly greater than the cutoff. User messages are skipped on
// purpose: the marker is about what the agent did since the user
// last looked, not what the user said.

import type { Message } from '../../lib/api';

/**
 * Find the oldest non-user message in `messages` whose timeCreated
 * is strictly greater than `cutoff`. Returns null when the cutoff
 * is non-positive (user has never seen the session — frontend
 * treats the whole session as fresh) or when no such message
 * exists.
 */
export function findFirstUnreadMessageId(
  messages: readonly Message[],
  cutoff: number,
): string | null {
  if (cutoff <= 0) return null;
  for (const m of messages) {
    if (m.timeCreated > cutoff && m.data?.role !== 'user') {
      return m.id;
    }
  }
  return null;
}

/**
 * Count non-user messages with timeCreated strictly greater than
 * `cutoff`. Mirrors findFirstUnreadMessageId: returns 0 for
 * non-positive cutoffs.
 */
export function countUnreadMessages(
  messages: readonly Message[],
  cutoff: number,
): number {
  if (cutoff <= 0) return 0;
  let count = 0;
  for (const m of messages) {
    if (m.timeCreated > cutoff && m.data?.role !== 'user') count++;
  }
  return count;
}
