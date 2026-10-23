import './SessionTable.css';
import './PhoneTables.css';
import type { Session } from '../lib/api';
import { filterVisibleSessions } from '../lib/sessionVisibility';
import { nestSessions } from '../lib/nestSessions';
import { SessionTableSkeleton } from './Skeleton';
import { DataTable } from './DataTable';
import { SessionTableRow } from './SessionTableRow';
import { useSessionTableArchive } from './useSessionTableArchive';
import { useIsPhone } from '../lib/useIsPhone';
import { InlineAlert } from './InlineAlert';
export { UnreadBadge, ShortPath, GitStatusLine } from './SessionTableDetails';

interface Props {
  sessions: Session[];
  showProject: boolean;
  loading?: boolean;
  includeArchived?: boolean;
  /** The caller already applied the shared archive/snooze/pending-prompt rules. */
  prefiltered?: boolean;
}

export function SessionTable({ sessions, showProject, loading, includeArchived, prefiltered }: Props) {
  const phone = useIsPhone();
  const { archivingSessionIds, locallyArchivedSessionIds, archiveError, handleArchiveSession } = useSessionTableArchive();

  if (loading) {
    return <SessionTableSkeleton rows={5} showProject={showProject} />;
  }

  const colCount = showProject ? 5 : 4;
  const visibleSessions = (includeArchived || prefiltered ? sessions : filterVisibleSessions(sessions))
    .filter(session => !phone || !archivingSessionIds.has(session.id))
    .filter(session => includeArchived || !locallyArchivedSessionIds.has(session.id));

  if (!visibleSessions.length) {
    return (
      <DataTable className="phone-session-table">
        <tbody>
          <tr>
            <td colSpan={colCount} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
              {includeArchived ? 'No sessions found' : 'No active sessions found'}
            </td>
          </tr>
        </tbody>
      </DataTable>
    );
  }

  return (
    <>
    {archiveError && <InlineAlert>{archiveError}</InlineAlert>}
    <DataTable className="phone-session-table">
      <thead>
        <tr>
          <th>Session</th>
          {showProject && <th>Project</th>}
          <th>Activity</th>
          <th>Started</th>
          <th style={{ width: 44 }} />
        </tr>
      </thead>
      <tbody>
        {nestSessions(visibleSessions).map(({ session, depth }) => <SessionTableRow
          key={session.id} session={session} depth={depth} showProject={showProject}
          archiving={archivingSessionIds.has(session.id)} onArchive={(event, row) => { void handleArchiveSession(event, row); }} />)}
      </tbody>
    </DataTable>
    </>
  );
}
