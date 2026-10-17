import { useMemo, useState } from 'react';
import './SessionTable.css';
import './PhoneTables.css';
import type { Session, Project } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { StatusBadge } from './StatusBadge';
import { HostBadge } from './HostBadge';
import { ProjectLabel } from './ProjectLabel';
import { filterVisibleSessions } from '../lib/sessionVisibility';
import { nestSessions } from '../lib/nestSessions';
import { SessionTableSkeleton } from './Skeleton';
import { DataTable, DataTableGroupHeader } from './DataTable';
import { projectIdentityIndex } from '../lib/projectIdentity';
import { rollupGroupStatus } from '../lib/sidebarHelpers';
import { remoteLog } from '../lib/remoteLog';
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

interface GroupedProps {
  sessions: Session[];
  loading?: boolean;
  includeArchived?: boolean;
  /** Set of project directory keys currently collapsed. */
  collapsedProjects: ReadonlySet<string>;
  /** Toggle the collapsed state of a project key. */
  toggleCollapsedProject: (directory: string) => void;
  /**
   * Known projects (from /api/projects). Drives two things: the
   * server-side `archived` flag per project root, and "add session"
   * placeholder rows for non-archived projects that currently have no
   * visible sessions. Optional: callers that don't pass it (e.g. the
   * session sidebar) just get session-derived groups with no
   * placeholders.
   */
  projects?: Project[];
  /** Called when a placeholder project's "Add session" is clicked. */
  onAddSession?: (directory: string, remoteId?: string) => void;
  /**
   * Show placeholder rows for known projects with no visible sessions.
   * The dashboard Sessions tab sets this false so it only lists projects
   * that actually have a session. Defaults to true.
   */
  showEmptyProjects?: boolean;
}

function ProjectMenuIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
      <circle cx="8" cy="3" r="1.4" fill="currentColor" />
      <circle cx="8" cy="8" r="1.4" fill="currentColor" />
      <circle cx="8" cy="13" r="1.4" fill="currentColor" />
    </svg>
  );
}

/**
 * Renders sessions grouped by their project root directory, with
 * collapsible group headers. Uses the same `collapsedProjects` /
 * `toggleCollapsedProject` state as the sidebar "projects" view so
 * collapse state is shared between both places.
 */
export function GroupedSessionTable({
  sessions,
  loading,
  includeArchived,
  collapsedProjects,
  toggleCollapsedProject,
  projects,
  onAddSession,
  showEmptyProjects = true,
}: GroupedProps) {
  const phone = useIsPhone();
  const { archivingSessionIds, locallyArchivedSessionIds, archiveError, handleArchiveSession } = useSessionTableArchive();
  const archiveProject = useApiStore((state) => state.archiveProject);
  // Optimistic project archive overlay so the UI reacts before the next
  // /api/projects refetch lands. Maps folded root -> archived?.
  const [localProjectArchived, setLocalProjectArchived] = useState<Map<string, boolean>>(new Map());
  const identity = useMemo(() => projectIdentityIndex(projects ?? []), [projects]);

  const handleArchiveProject = async (directory: string, archived: boolean, remoteId?: string) => {
    const key = identity(directory, remoteId).key;
    const members = (projects ?? []).filter(p => identity(p.directory, p.remoteId).key === key);
    setLocalProjectArchived(prev => new Map(prev).set(key, archived));
    try {
      await Promise.all((members.length ? members : [{ directory, remoteId }]).map(p =>
        archiveProject(p.directory, archived, p.remoteId || 'local')));
    } catch (err) {
      remoteLog.error('Failed to archive project', err);
      setLocalProjectArchived(prev => {
        const next = new Map(prev);
        next.delete(key);
        return next;
      });
    }
  };

  // Folded roots the server (or our optimistic overlay) marks archived.
  const archivedRoots = useMemo(() => {
    const set = new Set<string>();
    for (const p of projects ?? []) {
      if (p.archived) set.add(identity(p.directory, p.remoteId).key);
    }
    for (const p of projects ?? []) {
      if (!p.archived) set.delete(identity(p.directory, p.remoteId).key);
    }
    for (const [root, archived] of localProjectArchived) {
      if (archived) set.add(root); else set.delete(root);
    }
    return set;
  }, [projects, localProjectArchived, identity]);

  const groups = useMemo(() => {
    const visible = (includeArchived ? sessions : filterVisibleSessions(sessions))
      .filter(s => !phone || !archivingSessionIds.has(s.id))
      .filter(s => includeArchived || !locallyArchivedSessionIds.has(s.id));

    const buckets = new Map<string, Session[]>();
    for (const s of visible) {
      const key = identity(s.directory || '', s.remoteId).key;
      const existing = buckets.get(key);
      if (existing) existing.push(s);
      else buckets.set(key, [s]);
    }

    const sessionGroups = Array.from(buckets.entries())
      .map(([key, groupSessions]) => {
        const sorted = [...groupSessions].sort((a, b) => b.timeUpdated - a.timeUpdated);
        const target = identity(sorted[0].directory || '', sorted[0].remoteId);
        return {
          ...target,
          remoteId: target.remoteId || 'local',
          remoteName: target.remoteName ?? sorted.find(s => (s.remoteId || 'local') === (target.remoteId || 'local'))?.remoteName,
          key,
          sessions: sorted,
          lastUpdated: sorted[0]?.timeUpdated ?? 0,
          aggregate: rollupGroupStatus(sorted),
          placeholder: false,
        };
      });

    // Placeholder rows: known non-archived projects that currently have
    // no session group. Sourced from /api/projects (folded to roots).
    // Only shown alongside real session groups — when the whole list is
    // empty we fall through to the plain "No sessions found" empty state
    // rather than papering it over with project placeholders.
    const haveSessions = new Set(sessionGroups.map(g => g.key));
    const placeholders: typeof sessionGroups = [];
    const seenRoots = new Set<string>();
    if (showEmptyProjects && sessionGroups.length > 0) {
      for (const p of projects ?? []) {
        const target = identity(p.directory, p.remoteId);
        if (haveSessions.has(target.key) || seenRoots.has(target.key)) continue;
        if (archivedRoots.has(target.key)) continue;
        seenRoots.add(target.key);
        placeholders.push({
          ...target,
          remoteId: target.remoteId || 'local',
          remoteName: target.remoteName,
          sessions: [],
          lastUpdated: p.lastUsed,
          aggregate: rollupGroupStatus([]),
          placeholder: true,
        });
      }
    }

    return [...sessionGroups, ...placeholders]
      // Hide archived projects' session groups unless showing archived.
      .filter(g => includeArchived || !archivedRoots.has(g.key))
      .sort((a, b) => b.lastUpdated - a.lastUpdated);
  }, [sessions, includeArchived, locallyArchivedSessionIds, projects, archivedRoots, showEmptyProjects, identity, phone, archivingSessionIds]);

  if (loading) {
    return <SessionTableSkeleton rows={5} showProject={false} />;
  }

  if (groups.length === 0) {
    return (
      <DataTable className="phone-session-table">
        <tbody>
          <tr>
            <td colSpan={4} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
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
      {groups.map(group => {
        const collapsed = collapsedProjects.has(group.key);
        const agg = group.aggregate;
        const remoteSession = group.sessions.find(s => s.remoteId && s.remoteId !== 'local');
        const dotStatus: Session['status'] =
          agg.kind === 'none' ? 'done' :
          agg.kind === 'pending' ? 'waiting' :
          agg.kind;
        const dotPending = agg.kind === 'pending';
        const archived = archivedRoots.has(group.key);

        return (
          <div key={group.key || '__empty__'} className="oc-session-group">
            <DataTableGroupHeader className="oc-session-group-header-row">
              <button
                type="button"
                className={`oc-session-group-header${collapsed ? ' collapsed' : ''}`}
                aria-expanded={!collapsed}
                title={group.directory || 'Unknown project'}
                onClick={() => toggleCollapsedProject(group.key)}
              >
                <span className="oc-session-group-status">
                  <StatusBadge status={dotStatus} compact pending={dotPending} />
                </span>
                <span className="oc-session-group-label">
                  <HostBadge remoteName={group.remoteName} remoteId={group.remoteId} stale={remoteSession?.stale} />
                  <ProjectLabel path={group.directory} />
                </span>
                <span className="oc-session-group-count">{group.sessions.length}</span>
              </button>
              {group.directory && (
                <details className="oc-project-menu">
                  <summary
                    className="oc-project-menu-trigger"
                    title="Project actions"
                    aria-label="Project actions"
                  >
                    <ProjectMenuIcon />
                  </summary>
                  <div className="oc-project-menu-list" role="menu">
                    {onAddSession && (
                      <button
                        type="button"
                        role="menuitem"
                        className="oc-project-menu-item"
                        onClick={(e) => {
                          (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
                          onAddSession(group.directory, group.remoteId);
                        }}
                      >Add session</button>
                    )}
                    <button
                      type="button"
                      role="menuitem"
                      className="oc-project-menu-item"
                      onClick={(e) => {
                        (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
                        void handleArchiveProject(group.directory, !archived, group.remoteId);
                      }}
                    >{archived ? 'Unarchive project' : 'Archive project'}</button>
                  </div>
                </details>
              )}
            </DataTableGroupHeader>
            {group.placeholder ? (
              !collapsed && (
                <div className="oc-session-group-placeholder">
                  <span>No active sessions</span>
                  {onAddSession && (
                    <button
                      type="button"
                      className="oc-session-group-placeholder-add"
                      onClick={() => onAddSession(group.directory, group.remoteId)}
                    >
                      <i className="bi bi-plus-lg" aria-hidden="true" /> Add session
                    </button>
                  )}
                </div>
              )
            ) : !collapsed && (
              <DataTable className="phone-session-table">
                <thead>
                  <tr>
                    <th>Session</th>
                    <th>Activity</th>
                    <th>Started</th>
                    <th style={{ width: 44 }} />
                  </tr>
                </thead>
                <tbody>
                  {nestSessions(group.sessions).map(({ session, depth }) => <SessionTableRow
                    key={session.id} session={session} depth={depth} archiving={archivingSessionIds.has(session.id)}
                    onArchive={(event, row) => { void handleArchiveSession(event, row); }} />)}
                </tbody>
              </DataTable>
            )}
          </div>
        );
      })}
    </>
  );
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
