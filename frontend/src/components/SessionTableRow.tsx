import { useState, type CSSProperties, type MouseEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import type { Session } from '../lib/api';
import { cleanTitle, formatDuration, relativeTime } from '../lib/format';
import { isTerminalStatus } from '../lib/sessionStatus';
import { useIsPhone } from '../lib/useIsPhone';
import { useSessionSwipe } from '../lib/useSessionSwipe';
import { StatusBadge } from './StatusBadge';
import { PhoneSessionMeta } from './PhoneSessionMeta';
import { ShortPath, UnreadBadge } from './SessionTableDetails';
import { ArchiveButton } from './ArchiveButton';
import { SnoozeButton } from './SnoozeButton';
import './SessionSwipe.css';

interface Props {
  session: Session;
  depth: number;
  showProject?: boolean;
  archiving: boolean;
  activityPending?: boolean;
  onArchive: (event: MouseEvent, session: Session) => void;
}

/** Shared by flat and grouped tables; desktop keeps its existing action cell. */
export function SessionTableRow({ session, depth, showProject, archiving, onArchive, activityPending }: Props) {
  const navigate = useNavigate();
  const phone = useIsPhone();
  const [snoozeOpen, setSnoozeOpen] = useState(false);
  const [snoozing, setSnoozing] = useState(false);
  const swipe = useSessionSwipe({ disabled: archiving || snoozeOpen || snoozing,
    onArchive: event => onArchive(event, session), onSnooze: () => setSnoozeOpen(true) });
  return <tr {...swipe}
    aria-busy={archiving || snoozing}
    className={`session-swipe-row ${session.liveConnection ? '' : 'no-port'}${depth > 0 ? ' session-row-child' : ''}`}
    onClick={() => navigate(`/session/${session.id}`)}>
    <td>
      <Link className="session-row-link" to={`/session/${session.id}`} onClick={event => event.stopPropagation()}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          {depth > 0 && <span className="session-child-branch" style={{ '--depth': depth } as CSSProperties} aria-hidden="true">&#9492;&#9472;</span>}
          <StatusBadge status={session.status} resetsAt={session.resetsAt} compact
            seen={isTerminalStatus(session.status) && session.seen} pending={session.pendingPermission || session.pendingQuestion} />
          <span className="session-title">{cleanTitle(session.title) || 'Untitled'}</span>
          {!phone && <UnreadBadge session={session} />}
        </div>
        <PhoneSessionMeta session={session} />
        <div className="mono">{session.id}</div>
      </Link>
    </td>
    {showProject && <td className="mono"><ShortPath path={session.directory} /></td>}
    <td className="mono">{activityPending ? 'Loading activity…' : <>{session.messageCount} msgs &middot; {formatDuration(session.durationMs)}</>}</td>
    <td><span title={new Date(session.timeCreated).toLocaleString()}>{relativeTime(session.timeCreated)}</span></td>
    <td className="session-action-cell session-swipe-actions">
      {phone && <SnoozeButton session={session} disabled={archiving} open={snoozeOpen} onOpenChange={setSnoozeOpen} onBusyChange={setSnoozing} />}
      <ArchiveButton iconSize={14} label="Archive session" title="Archive session (reappears on new activity)"
        onClick={event => onArchive(event, session)} disabled={archiving} />
    </td>
  </tr>;
}
