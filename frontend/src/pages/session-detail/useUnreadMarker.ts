import { useMemo, useState } from 'react';
import type { Message } from '../../lib/api';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { countUnreadMessages, findFirstUnreadMessageId } from './unreadMarker';

export interface UseUnreadMarkerResult {
  firstUnreadMessageId: string | null;
  unreadMessageCount: number;
}

/**
 * Snapshot of the user's last-seen cutoff for the current session,
 * captured once per session id. Later updates (markSessionSeen, SSE)
 * do NOT move the cutoff so the "first unread" marker and the "N new
 * messages" pill stay put after the persisted seen state advances.
 *
 * Stored as state keyed on the session id (derived-state-during-render
 * pattern) rather than a ref so it resets on navigation without an
 * effect and render-time reads stay lint-clean.
 */
export function useUnreadMarker(session: SessionMetadata | null, messages: Message[]): UseUnreadMarkerResult {
  const [cutoffState, setCutoffState] = useState<{ sessionId: string; cutoff: number } | null>(null);
  if (session && cutoffState?.sessionId !== session.id) {
    setCutoffState({ sessionId: session.id, cutoff: session.seenTimeUpdated || 0 });
  }
  const cutoff = cutoffState?.sessionId === session?.id ? (cutoffState?.cutoff ?? 0) : 0;
  const firstUnreadMessageId = useMemo(
    () => session ? findFirstUnreadMessageId(messages, cutoff) : null,
    [session, messages, cutoff],
  );
  const unreadMessageCount = useMemo(
    () => firstUnreadMessageId ? countUnreadMessages(messages, cutoff) : 0,
    [messages, firstUnreadMessageId, cutoff],
  );
  return { firstUnreadMessageId, unreadMessageCount };
}
