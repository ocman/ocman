import { useState, type CSSProperties, type MouseEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import type { Session } from '../lib/api';
import { cleanTitle, formatCount, formatDuration } from '../lib/format';
import { formatCost, sessionActivityDetails, sessionCost } from '../lib/listActivity';
import { isTerminalStatus } from '../lib/sessionStatus';
import { useIsPhone } from '../lib/useIsPhone';
import { useSessionSwipe } from '../lib/useSessionSwipe';
import { StatusBadge } from './StatusBadge';
import { PhoneSessionMeta } from './PhoneSessionMeta';
import { ShortPath } from './SessionTableDetails';
import { ArchiveButton } from './ArchiveButton';
import { CopyButton } from './CopyButton';
import { RelativeTimestamp } from './RelativeTime';
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
  const title = cleanTitle(session.title) || 'Untitled';
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
          <span className="session-title" title={title}>{title}</span>
        </div>
        <PhoneSessionMeta session={session} activity={activityPending ? undefined
          : `${formatCount(session.messageCount, 'prompt')} \u00b7 ${formatDuration(session.durationMs)} \u00b7 ${formatCost(sessionCost(session))}`} />
      </Link>
      <div className="session-row-id" onClick={event => event.stopPropagation()}>
        <span className="mono">{session.id}</span>
        <CopyButton iconOnly variant="ghost" text={session.id} label="Copy session ID" />
      </div>
    </td>
    {showProject && <td className="mono"><ShortPath path={session.directory} /></td>}
    {activityPending ? <td colSpan={3} className="list-numeric-pending">Loading activity…</td> : <>
      <td className="mono list-numeric" title={formatCount(session.messageCount, 'prompt')}>{session.messageCount.toLocaleString('en-US')}</td>
      <td className="mono list-numeric" title={sessionActivityDetails(session)}>{formatDuration(session.durationMs)}</td>
      <td className="mono list-numeric">{formatCost(sessionCost(session))}</td>
    </>}
    <td><RelativeTimestamp value={session.timeCreated} /></td>
    <td className="session-action-cell session-swipe-actions">
      {phone && <SnoozeButton session={session} disabled={archiving} open={snoozeOpen} onOpenChange={setSnoozeOpen} onBusyChange={setSnoozing} />}
      <ArchiveButton iconSize={14} label="Archive session" title="Archive session (reappears on new activity)"
        onClick={event => onArchive(event, session)} disabled={archiving} />
    </td>
  </tr>;
}
