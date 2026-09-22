import type React from 'react';
import type { GitInfo, Session } from '../../lib/api';
import { cleanTitle, relativeTime } from '../../lib/format';
import { projectRootForDirectory } from '../../lib/worktrees';
import { isTerminalStatus } from '../../lib/sessionStatus';
import { StatusBadge } from '../../components/StatusBadge';
import { ShortPath, GitStatusLine } from '../../components/SessionTable';
import { remoteLog } from '../../lib/remoteLog';
import { ArchiveIcon } from './SidebarIcons';

export interface SidebarSessionRowProps {
  session: Session;
  /** Rendered under a directory sub-header (no own project/git line). */
  inGroup: boolean;
  /** Flat recent rows carry their own project and branch context. */
  flat?: boolean;
  /** Nesting depth for child sessions. */
  depth?: number;
  active: boolean;
  activeId: string | undefined;
  /** SSE-derived status for the active row; the poll value lags it. */
  activeDisplayStatus: Session['status'];
  archiving: boolean;
  draft: boolean;
  debugMode: boolean;
  gitInfo: GitInfo | undefined;
  onNavigateToSession: (id: string) => void;
  onArchiveSession: (e: React.MouseEvent, session: Session) => void;
  onPinSession: (e: React.MouseEvent, session: Session) => void;
}

/** One session row in the sidebar (pinned or grouped view). */
export function SidebarSessionRow({
  session: sib,
  inGroup,
  flat = false,
  depth = 0,
  active,
  activeId,
  activeDisplayStatus,
  archiving,
  draft,
  debugMode,
  gitInfo,
  onNavigateToSession,
  onArchiveSession,
  onPinSession,
}: SidebarSessionRowProps) {
  // For the currently-viewed session we trust the SSE-derived status
  // over the last poll (OpenCode's DB can lag SSE by several seconds;
  // using the poll value here would leave the sidebar pulse running
  // after the composer has already gone idle).
  const displayStatus = active ? activeDisplayStatus : sib.status;
  // Grouped rows no longer carry their own git line — the directory
  // sub-header above them shows the branch/worktree once for all
  // siblings. Ungrouped rows (the pinned group) keep the project
  // path + git line since they have no directory sub-header.
  const projectRoot = projectRootForDirectory(sib.directory || '');
  const isWorktree = !!sib.directory && sib.directory !== projectRoot;
  const statusBadge = (
    <StatusBadge
      status={displayStatus}
      compact
      seen={isTerminalStatus(displayStatus) && sib.seen}
      pending={sib.pendingPermission || sib.pendingQuestion}
      draft={draft}
      titleOverride={sib.notice?.message}
    />
  );
  return (
    <div
      role="button"
      data-session-key={`${inGroup ? 'group' : 'recent'}:${sib.platform}:${sib.id}`}
      tabIndex={0}
      aria-selected={active}
      className={`session-sidebar-item ${active ? 'active' : ''}${archiving ? ' archiving' : ''}${inGroup ? ' in-group' : ''}${flat ? ' flat' : ''}${depth > 0 ? ' session-sidebar-item-child' : ''}`}
      onClick={() => {
        if (debugMode) {
          remoteLog.info('[ocman:nav] sidebar click', {
            from: activeId,
            to: sib.id,
            at: performance.now(),
          });
        }
        onNavigateToSession(sib.id);
      }}
      // Middle click archives the row. preventDefault on mousedown stops
      // the browser's middle-click autoscroll from kicking in.
      onMouseDown={(e) => { if (e.button === 1) e.preventDefault(); }}
      onAuxClick={(e) => {
        if (e.button !== 1) return;
        e.preventDefault();
        onArchiveSession(e, sib);
      }}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          if (debugMode) {
            remoteLog.info('[ocman:nav] sidebar key', {
              from: activeId,
              to: sib.id,
              at: performance.now(),
            });
          }
          onNavigateToSession(sib.id);
        }
      }}
    >
      {depth > 0 && (
        <span
          className="session-child-branch"
          style={{ '--depth': depth } as React.CSSProperties}
          aria-hidden="true"
        >
          &#9492;&#9472;
        </span>
      )}
      {!flat && statusBadge}
      <span className="session-sidebar-item-body">
        {flat && (
          <span className="session-sidebar-project">
            {statusBadge}
            <span className="session-sidebar-project-path">
              <ShortPath path={isWorktree ? projectRoot : sib.directory} />
            </span>
            <span className="session-sidebar-time" title={new Date(sib.timeUpdated).toLocaleString()}>
              {relativeTime(sib.timeUpdated).replace('just now', 'now').replace(' ago', '')}
            </span>
          </span>
        )}
        <span className="session-sidebar-title">
          {cleanTitle(sib.title) || 'Untitled'}
        </span>
        {!inGroup && !flat && (
          <>
            <span className="session-sidebar-project">
              <span className="session-sidebar-project-path">
                <ShortPath path={isWorktree ? projectRoot : sib.directory} />
              </span>
            </span>
            <GitStatusLine info={gitInfo} icon={isWorktree ? 'worktree' : 'branch'} />
          </>
        )}
        {flat && (
          <span className="session-sidebar-git-slot">
            <GitStatusLine info={gitInfo} icon={isWorktree ? 'worktree' : 'branch'} />
          </span>
        )}
      </span>
      <span className="session-sidebar-meta">
        {!flat && (
          <span className="session-sidebar-time" title={new Date(sib.timeUpdated).toLocaleString()}>
            {relativeTime(sib.timeUpdated).replace('just now', 'now').replace(' ago', '')}
          </span>
        )}
        <span className="session-sidebar-actions">
          <button
            type="button"
            className={`session-pin-btn session-sidebar-pin-btn${sib.pinned ? ' pinned' : ''}`}
            onClick={(e) => onPinSession(e, sib)}
            title={sib.pinned ? 'Unpin session' : 'Pin session'}
            aria-label={sib.pinned ? 'Unpin session' : 'Pin session'}
          >
            <i className={`bi ${sib.pinned ? 'bi-pin-fill' : 'bi-pin'}`} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="session-archive-btn session-sidebar-archive-btn"
            onClick={(e) => onArchiveSession(e, sib)}
            title="Archive session"
            aria-label="Archive session"
            disabled={archiving}
          >
            <ArchiveIcon />
          </button>
        </span>
      </span>
    </div>
  );
}
