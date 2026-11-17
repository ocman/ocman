import './SessionTable.css';
import './PhoneTables.css';
import type { Session } from '../lib/api';
import { filterVisibleSessions } from '../lib/sessionVisibility';
import { nestSessions } from '../lib/nestSessions';
import { SkeletonTable } from './Skeleton';
import { DataTable } from './DataTable';
import { SessionTableRow } from './SessionTableRow';
import { useSessionTableArchive } from './useSessionTableArchive';
import { InlineAlert } from './InlineAlert';
import { SortableHeader } from './SortableHeader';
import { ACTIVITY_DEFINITIONS, sessionCost } from '../lib/listActivity';
import { sortBy, useUrlSort } from '../lib/useUrlSort';

const SORT_KEYS = ['prompts', 'duration', 'cost', 'started'] as const;
type SortKey = typeof SORT_KEYS[number];
const sortValue = (s: Session, key: SortKey) => ({ prompts: s.messageCount, duration: s.durationMs, cost: sessionCost(s), started: s.timeCreated })[key];
export { ShortPath, GitStatusLine } from './SessionTableDetails';

interface Props {
  sessions: Session[];
  showProject: boolean;
  loading?: boolean;
  includeArchived?: boolean;
  /** The caller already applied the shared archive/snooze/pending-prompt rules. */
  prefiltered?: boolean;
  /** Owner-qualified `${platform}:${id}` keys whose accounting is not ready. */
  activityPendingKeys?: ReadonlySet<string>;
}

export function SessionTable({ sessions, showProject, loading, includeArchived, prefiltered, activityPendingKeys }: Props) {
  const { archivingSessionIds, locallyArchivedSessionIds, archiveError, handleArchiveSession } = useSessionTableArchive();
  const sort = useUrlSort(SORT_KEYS);

  if (loading) {
    return <SkeletonTable columns={[...(showProject ? ['Session', 'Project'] : ['Session']), 'Prompts', 'Duration', 'Cost', 'Started', '']} label="Loading sessions" />;
  }

  const colCount = showProject ? 7 : 6;
  const visibleSessions = (includeArchived || prefiltered ? sessions : filterVisibleSessions(sessions))
    .filter(session => !archivingSessionIds.has(session.id))
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
          <SortableHeader column="prompts" sort={sort} numeric description={ACTIVITY_DEFINITIONS.prompts}>Prompts</SortableHeader>
          <SortableHeader column="duration" sort={sort} numeric description={ACTIVITY_DEFINITIONS.duration}>Duration</SortableHeader>
          <SortableHeader column="cost" sort={sort} numeric description={ACTIVITY_DEFINITIONS.cost}>Cost</SortableHeader>
          <SortableHeader column="started" sort={sort}>Started</SortableHeader>
          <th style={{ width: 44 }} />
        </tr>
      </thead>
      <tbody>
        {nestSessions(sortBy(visibleSessions, sort, sortValue)).map(({ session, depth }) => <SessionTableRow
          key={session.id} session={session} depth={depth} showProject={showProject}
          activityPending={activityPendingKeys?.has(`${session.platform}:${session.id}`)}
          archiving={archivingSessionIds.has(session.id)} onArchive={(event, row) => { void handleArchiveSession(event, row); }} />)}
      </tbody>
    </DataTable>
    </>
  );
}
