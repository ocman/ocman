import { useMemo, useState } from 'react';
import './SessionTable.css';
import { useNavigate } from 'react-router-dom';
import type { Session, GitInfo, Project } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { cleanTitle, formatDuration, relativeTime, shortPath } from '../lib/format';
import { StatusBadge } from './StatusBadge';
import { PlatformBadge } from './PlatformBadge';
import { HostBadge } from './HostBadge';
import { filterVisibleSessions } from '../lib/sessionVisibility';
import { nestSessions } from '../lib/nestSessions';
import { SessionTableSkeleton } from './Skeleton';
import { projectRootForDirectory } from '../lib/worktrees';
import { rollupGroupStatus } from '../lib/sidebarHelpers';
import { remoteLog } from '../lib/remoteLog';

/**
 * UnreadBadge renders a small "N new" pill on a session row when
 * the session has unread messages and is not currently active.
 * Returns null otherwise so it stays out of the layout entirely.
 *
 * Matches the "blue = new since last view" vocabulary used by the
 * status dot (see StatusBadge.css).
 */
export function UnreadBadge({ session }: { session: Session }) {
  if (!session.unreadCount || session.seen) return null;
  const label = session.unreadCount > 99 ? '99+' : String(session.unreadCount);
  return (
    <span
      className="session-unread-badge"
      data-testid="session-unread-badge"
      title={`${session.unreadCount} new message${session.unreadCount === 1 ? '' : 's'} since you last viewed this session`}
    >
      {label}
    </span>
  );
}

export function ShortPath({ path }: { path: string }) {
  const parts = (path || '').split('/').filter(Boolean);
  const last = parts.pop() || '';
  const prefix = parts.length > 0 ? parts.slice(-1).join('/') + '/' : '';
  return <><span className="short-path-prefix">{prefix}</span><span className="short-path-last">{last}</span></>;
}

export function GitStatusLine({ info, icon }: { info?: GitInfo | null; icon?: 'branch' | 'worktree' }) {
  if (!info || !info.branch) return null;
  const dirtyCls = info.dirty ? ' git-status-dirty' : '';
  const iconCls = icon === 'worktree' ? 'bi bi-diagram-2' : 'bi bi-git';
  return (
    <div className={`git-status${dirtyCls}`}>
      <i className={`${iconCls} git-status-icon`} aria-hidden="true" />
      <span className="git-status-branch" title={`Current branch: ${info.branch}`}>
        {info.branch}
      </span>
      {info.ahead > 0 && (
        <span
          className="git-status-ahead"
          title={`${info.ahead} commit${info.ahead === 1 ? '' : 's'} ahead of upstream (not yet pushed)`}
        >&uarr;{info.ahead}</span>
      )}
      {info.behind > 0 && (
        <span
          className="git-status-behind"
          title={`${info.behind} commit${info.behind === 1 ? '' : 's'} behind upstream (not yet pulled)`}
        >&darr;{info.behind}</span>
      )}
      {info.dirty && (
        <span
          className="git-status-mark"
          title="Working tree has uncommitted changes"
        >*</span>
      )}
    </div>
  );
}

function ArchiveIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2 3.5h12v2H2zm1 3h10v6H3zm3 2.5h4" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

interface Props {
  sessions: Session[];
  showProject: boolean;
  loading?: boolean;
  includeArchived?: boolean;
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
  onAddSession?: (directory: string) => void;
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
}: GroupedProps) {
  const navigate = useNavigate();
  const archiveSession = useApiStore((state) => state.archiveSession);
  const archiveProject = useApiStore((state) => state.archiveProject);
  const [archivingSessionIds, setArchivingSessionIds] = useState<Set<string>>(new Set());
  const [locallyArchivedSessionIds, setLocallyArchivedSessionIds] = useState<Set<string>>(new Set());
  // Optimistic project archive overlay so the UI reacts before the next
  // /api/projects refetch lands. Maps folded root -> archived?.
  const [localProjectArchived, setLocalProjectArchived] = useState<Map<string, boolean>>(new Map());

  const handleArchiveSession = async (e: React.MouseEvent, session: Session) => {
    e.stopPropagation();
    if (archivingSessionIds.has(session.id)) return;
    setArchivingSessionIds(prev => new Set(prev).add(session.id));
    try {
      await archiveSession(session.platform, session.id, session.timeUpdated, true);
      setLocallyArchivedSessionIds(prev => new Set(prev).add(session.id));
    } catch (err) {
      remoteLog.error('Failed to archive session', err);
    } finally {
      setArchivingSessionIds(prev => {
        const next = new Set(prev);
        next.delete(session.id);
        return next;
      });
    }
  };

  const handleArchiveProject = async (directory: string, archived: boolean) => {
    setLocalProjectArchived(prev => new Map(prev).set(directory, archived));
    try {
      await archiveProject(directory, archived);
    } catch (err) {
      remoteLog.error('Failed to archive project', err);
      setLocalProjectArchived(prev => {
        const next = new Map(prev);
        next.delete(directory);
        return next;
      });
    }
  };

  // Folded roots the server (or our optimistic overlay) marks archived.
  const archivedRoots = useMemo(() => {
    const set = new Set<string>();
    for (const p of projects ?? []) {
      if (p.archived) set.add(projectRootForDirectory(p.directory));
    }
    for (const [root, archived] of localProjectArchived) {
      if (archived) set.add(root); else set.delete(root);
    }
    return set;
  }, [projects, localProjectArchived]);

  const groups = useMemo(() => {
    const visible = (includeArchived ? sessions : filterVisibleSessions(sessions))
      .filter(s => includeArchived || !locallyArchivedSessionIds.has(s.id));

    const buckets = new Map<string, Session[]>();
    for (const s of visible) {
      const key = projectRootForDirectory(s.directory || '');
      const existing = buckets.get(key);
      if (existing) existing.push(s);
      else buckets.set(key, [s]);
    }

    const sessionGroups = Array.from(buckets.entries())
      .map(([directory, groupSessions]) => {
        const sorted = [...groupSessions].sort((a, b) => b.timeUpdated - a.timeUpdated);
        return {
          directory,
          sessions: sorted,
          lastUpdated: sorted[0]?.timeUpdated ?? 0,
          aggregate: rollupGroupStatus(sorted),
          placeholder: false,
        };
      });

    // Placeholder rows: known non-archived projects that currently have
    // no session group. Sourced from /api/projects (folded to roots).
    const haveSessions = new Set(sessionGroups.map(g => g.directory));
    const placeholders: typeof sessionGroups = [];
    const seenRoots = new Set<string>();
    for (const p of projects ?? []) {
      const root = projectRootForDirectory(p.directory);
      if (haveSessions.has(root) || seenRoots.has(root)) continue;
      if (archivedRoots.has(root)) continue;
      seenRoots.add(root);
      placeholders.push({
        directory: root,
        sessions: [],
        lastUpdated: p.lastUsed,
        aggregate: rollupGroupStatus([]),
        placeholder: true,
      });
    }

    return [...sessionGroups, ...placeholders]
      // Hide archived projects' session groups unless showing archived.
      .filter(g => includeArchived || !archivedRoots.has(g.directory))
      .sort((a, b) => b.lastUpdated - a.lastUpdated);
  }, [sessions, includeArchived, locallyArchivedSessionIds, projects, archivedRoots]);

  if (loading) {
    return <SessionTableSkeleton rows={5} showProject={false} />;
  }

  if (groups.length === 0) {
    return (
      <table>
        <tbody>
          <tr>
            <td colSpan={4} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
              {includeArchived ? 'No sessions found' : 'No active sessions found'}
            </td>
          </tr>
        </tbody>
      </table>
    );
  }

  return (
    <>
      {groups.map(group => {
        const collapsed = collapsedProjects.has(group.directory);
        const label = group.directory ? shortPath(group.directory) : '(unknown)';
        const agg = group.aggregate;
        const dotStatus: Session['status'] =
          agg.kind === 'none' ? 'done' :
          agg.kind === 'pending' ? 'waiting' :
          agg.kind;
        const dotPending = agg.kind === 'pending';
        const archived = archivedRoots.has(group.directory);

        return (
          <div key={group.directory || '__empty__'} className="oc-session-group">
            <div className="oc-session-group-header-row">
              <button
                type="button"
                className={`oc-session-group-header${collapsed ? ' collapsed' : ''}`}
                aria-expanded={!collapsed}
                title={group.directory || 'Unknown project'}
                onClick={() => toggleCollapsedProject(group.directory)}
              >
                <span className="oc-session-group-status">
                  <StatusBadge status={dotStatus} compact pending={dotPending} />
                </span>
                <span className="oc-session-group-label">{label}</span>
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
                          onAddSession(group.directory);
                        }}
                      >Add session</button>
                    )}
                    <button
                      type="button"
                      role="menuitem"
                      className="oc-project-menu-item"
                      onClick={(e) => {
                        (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
                        void handleArchiveProject(group.directory, !archived);
                      }}
                    >{archived ? 'Unarchive project' : 'Archive project'}</button>
                  </div>
                </details>
              )}
            </div>
            {group.placeholder ? (
              !collapsed && (
                <div className="oc-session-group-placeholder">
                  <span>No active sessions</span>
                  {onAddSession && (
                    <button
                      type="button"
                      className="oc-session-group-placeholder-add"
                      onClick={() => onAddSession(group.directory)}
                    >
                      <i className="bi bi-plus-lg" aria-hidden="true" /> Add session
                    </button>
                  )}
                </div>
              )
            ) : !collapsed && (
              <table>
                <thead>
                  <tr>
                    <th>Session</th>
                    <th>Activity</th>
                    <th>Started</th>
                    <th style={{ width: 44 }} />
                  </tr>
                </thead>
                <tbody>
                  {nestSessions(group.sessions).map(({ session: s, depth }) => {
                    const seenLatest = (s.status === 'waiting' || s.status === 'error' || s.status === 'done') && s.seen;
                    const pending = s.pendingPermission || s.pendingQuestion;
                    return (
                      <tr
                        key={s.id}
                        className={`${s.liveConnection ? '' : 'no-port'}${depth > 0 ? ' session-row-child' : ''}`}
                        onClick={() => navigate(`/session/${s.id}`)}
                      >
                        <td>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                            {depth > 0 && (
                              <span className="session-child-branch" style={{ '--depth': depth } as React.CSSProperties} aria-hidden="true">&#9492;&#9472;</span>
                            )}
                            <StatusBadge status={s.status} compact seen={seenLatest} pending={pending} />
                            <span className="session-title">{cleanTitle(s.title) || 'Untitled'}</span>
                            <UnreadBadge session={s} />
                          </div>
                          <div className="mono">
                            <PlatformBadge platform={s.platform} variant="plain" />
                            {' '}
                            <HostBadge remoteName={s.remoteName} stale={s.stale} />
                            {' '}
                            {s.id}
                          </div>
                        </td>
                        <td className="mono">{s.messageCount} msgs &middot; {formatDuration(s.durationMs)}</td>
                        <td><span title={new Date(s.timeCreated).toLocaleString()}>{relativeTime(s.timeCreated)}</span></td>
                        <td className="session-action-cell">
                          <button
                            className="session-archive-btn"
                            onClick={(e) => { void handleArchiveSession(e, s); }}
                            title="Archive session (reappears on new activity)"
                            aria-label="Archive session"
                            disabled={archivingSessionIds.has(s.id)}
                          >
                            <ArchiveIcon />
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </div>
        );
      })}
    </>
  );
}

export function SessionTable({ sessions, showProject, loading, includeArchived }: Props) {
  const navigate = useNavigate();
  const archiveSession = useApiStore((state) => state.archiveSession);
  const [archivingSessionIds, setArchivingSessionIds] = useState<Set<string>>(new Set());
  const [locallyArchivedSessionIds, setLocallyArchivedSessionIds] = useState<Set<string>>(new Set());

  const handleArchiveSession = async (e: React.MouseEvent, session: Session) => {
    e.stopPropagation();
    if (archivingSessionIds.has(session.id)) return;

    setArchivingSessionIds(prev => new Set(prev).add(session.id));
    try {
      await archiveSession(session.platform, session.id, session.timeUpdated, true);
      setLocallyArchivedSessionIds(prev => new Set(prev).add(session.id));
    } catch (err) {
      remoteLog.error('Failed to archive session', err);
    } finally {
      setArchivingSessionIds(prev => {
        const next = new Set(prev);
        next.delete(session.id);
        return next;
      });
    }
  };

  if (loading) {
    return <SessionTableSkeleton rows={5} showProject={showProject} />;
  }

  const colCount = showProject ? 5 : 4;
  const visibleSessions = (includeArchived ? sessions : filterVisibleSessions(sessions))
    .filter(session => includeArchived || !locallyArchivedSessionIds.has(session.id));

  if (!visibleSessions.length) {
    return (
      <table>
        <tbody>
          <tr>
            <td colSpan={colCount} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: 24 }}>
              {includeArchived ? 'No sessions found' : 'No active sessions found'}
            </td>
          </tr>
        </tbody>
      </table>
    );
  }

  return (
    <table>
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
        {nestSessions(visibleSessions).map(({ session: s, depth }) => {
          const seenLatest = (s.status === 'waiting' || s.status === 'error' || s.status === 'done') && s.seen;
          const pending = s.pendingPermission || s.pendingQuestion;
          return (
            <tr
              key={s.id}
              className={`${s.liveConnection ? '' : 'no-port'}${depth > 0 ? ' session-row-child' : ''}`}
              onClick={() => navigate(`/session/${s.id}`)}
            >
              <td>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                  {depth > 0 && (
                    <span className="session-child-branch" style={{ '--depth': depth } as React.CSSProperties} aria-hidden="true">&#9492;&#9472;</span>
                  )}
                  <StatusBadge status={s.status} compact seen={seenLatest} pending={pending} />
                  <span className="session-title">{cleanTitle(s.title) || 'Untitled'}</span>
                  <UnreadBadge session={s} />
                </div>
                <div className="mono">
                  <PlatformBadge platform={s.platform} variant="plain" />
                  {' '}
                  <HostBadge remoteName={s.remoteName} stale={s.stale} />
                  {' '}
                  {s.id}
                </div>
              </td>
              {showProject && (
                 <td className="mono">
                   {/*
                     Git status was previously rendered here, populated
                     by the backend's /api/sessions handler running
                     `git status` per directory on every dashboard
                     poll. That fork-fan-out caused multi-second
                     pauses across unrelated handlers (see
                     docs/profiling.md), and dashboard branch
                     indicators aren't worth that cost. Branch info
                     still appears in SessionDetail's right-hand
                     sidebar and in the sibling-sessions list there;
                     both fetch it via /api/git/info on demand.
                   */}
                   <ShortPath path={s.directory} />
                 </td>
              )}
              <td className="mono">{s.messageCount} msgs &middot; {formatDuration(s.durationMs)}</td>
              <td><span title={new Date(s.timeCreated).toLocaleString()}>{relativeTime(s.timeCreated)}</span></td>
              <td className="session-action-cell">
                <button
                  className="session-archive-btn"
                  onClick={(e) => handleArchiveSession(e, s)}
                  title="Archive session (reappears on new activity)"
                  aria-label="Archive session"
                  disabled={archivingSessionIds.has(s.id)}
                >
                  <ArchiveIcon />
                </button>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}