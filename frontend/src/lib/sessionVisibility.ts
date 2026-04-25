type SessionLike = {
  archived?: boolean;
};

// filterVisibleSessions drops archived rows. Accepts null/undefined
// because /api/sessions occasionally serializes a Go nil slice as JSON
// `null` instead of `[]`, which used to crash the dashboard with
// `null is not an object (evaluating 'sessions.length')` once the value
// reached this helper. Returning [] in that case keeps callers
// interchangeable with the happy-path array.
export function filterVisibleSessions<T extends SessionLike>(
  sessions: T[] | null | undefined,
): T[] {
  if (!sessions || !sessions.length) return sessions ?? [];

  return sessions.filter(session => !session.archived);
}
