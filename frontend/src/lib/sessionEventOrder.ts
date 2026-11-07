import type { Session } from './api';

const revisions = new Map<string, number>();

// Revisions order compact reads and live patches within one server connection.
// They are not contiguous cursors; reconnect still reconciles a full snapshot.
export function acceptSessionEvent(event: { sessionID?: string; platform?: string; revision?: number }): boolean {
  if (event.revision === undefined) return true; // Older owners remain supported.
  if (!event.platform || !event.sessionID || !Number.isSafeInteger(event.revision) || event.revision <= 0) return false;
  const key = JSON.stringify([event.platform, event.sessionID]);
  if ((revisions.get(key) ?? 0) >= event.revision) return false;
  revisions.delete(key);
  revisions.set(key, event.revision);
  if (revisions.size > 4096) revisions.delete(revisions.keys().next().value!);
  return true;
}

export function resetSessionEventOrder() { revisions.clear(); }

export function isCompactSessionSnapshot(session?: Session, patch?: Partial<Session>): session is Session {
  return !!session && !!session.platform && patch?.id === session.id && patch.platform === session.platform;
}
