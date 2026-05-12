import { useState } from 'react';
import './SessionTable.css';
import { useNavigate } from 'react-router-dom';
import type { Session, GitInfo } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { cleanTitle, formatDuration, relativeTime } from '../lib/format';
import { StatusBadge } from './StatusBadge';
import { PlatformBadge } from './PlatformBadge';
import { filterVisibleSessions } from '../lib/sessionVisibility';
import { SessionTableSkeleton } from './Skeleton';

export function ShortPath({ path }: { path: string }) {
  const parts = (path || '').split('/').filter(Boolean);
  const last = parts.pop() || '';
  const prefix = parts.length > 0 ? parts.slice(-1).join('/') + '/' : '';
  return <><span className="short-path-prefix">{prefix}</span><span className="short-path-last">{last}</span></>;
}

export function GitStatusLine({ info }: { info?: GitInfo | null }) {
  if (!info || !info.branch) return null;
  const dirtyCls = info.dirty ? ' git-status-dirty' : '';
  return (
    <div className={`git-status${dirtyCls}`}>
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
      console.error('Failed to archive session', err);
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
        {visibleSessions.map(s => {
          const seenLatest = (s.status === 'waiting' || s.status === 'error' || s.status === 'done') && s.seen;
          const pending = s.pendingPermission || s.pendingQuestion;
          return (
            <tr
              key={s.id}
              className={s.liveConnection ? '' : 'no-port'}
              onClick={() => navigate(`/session/${s.id}`)}
            >
              <td>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                  <StatusBadge status={s.status} compact seen={seenLatest} pending={pending} />
                  <span className="session-title">{cleanTitle(s.title) || 'Untitled'}</span>
                </div>
                <div className="mono">
                  <PlatformBadge platform={s.platform} variant="plain" />
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