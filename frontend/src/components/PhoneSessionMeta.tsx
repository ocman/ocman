import type { Session } from '../lib/api';
import { relativeTime } from '../lib/format';
import { HostBadge } from './HostBadge';

/** Phone-only context, alongside the desktop table's unchanged columns. */
export function PhoneSessionMeta({ session }: { session: Session }) {
  const status = session.pendingPermission ? 'Permission needed'
    : session.pendingQuestion ? 'Question for you' : null;
  const project = session.directory.split('/').filter(Boolean).pop() || 'Unknown project';
  return <span className="phone-session-meta">
    <span className="phone-session-project"><HostBadge remoteId={session.remoteId} remoteName={session.remoteName} stale={session.stale} />{project}</span>
    {status && <span className="phone-session-pending">{status}</span>}
    <time dateTime={new Date(session.timeUpdated).toISOString()}>{relativeTime(session.timeUpdated)}</time>
  </span>;
}
