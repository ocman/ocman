type SessionLike = {
  archived?: boolean;
};

export function filterVisibleSessions<T extends SessionLike>(sessions: T[]): T[] {
  if (!sessions.length) return sessions;

  return sessions.filter(session => !session.archived);
}
