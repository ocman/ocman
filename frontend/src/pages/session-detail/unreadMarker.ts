// Find the first-unread marker without counting messages or loading the
// SessionDetail React tree.
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
