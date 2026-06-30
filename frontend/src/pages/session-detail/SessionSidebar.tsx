import type React from 'react';
import { useRef, useEffect, useCallback, useMemo } from 'react';
import {
  DndContext,
  KeyboardSensor,
  MouseSensor,
  TouchSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import type { Session } from '../../lib/api';
import { cleanTitle, shortPath, relativeTime } from '../../lib/format';
import { projectRootForDirectory } from '../../lib/worktrees';
import { StatusBadge } from '../../components/StatusBadge';
import { PlatformBadge } from '../../components/PlatformBadge';
import { ShortPath, GitStatusLine } from '../../components/SessionTable';
import { BackendStats } from '../../components/BackendStats';
import { SidebarResizer } from '../../components/SidebarResizer';
import { SessionSidebarListSkeleton } from '../../components/Skeleton';
import { GettingStartedEmpty } from '../../components/GettingStartedEmpty';
import { rollupGroupStatus } from '../../lib/sidebarHelpers';
import { nestSessions } from '../../lib/nestSessions';
import { remoteLog } from '../../lib/remoteLog';
import { ArchiveIcon, ArchiveFilterIcon, ProjectsViewIcon, RecentViewIcon } from './SidebarIcons';
import type { TmuxState } from '../../lib/useTmux';
import type { GitInfo } from '../../lib/api';

export interface SidebarProjectGroup {
  directory: string;
  sessions: Session[];
  lastUpdated: number;
  aggregate: ReturnType<typeof rollupGroupStatus>;
  isPinned?: boolean;
}

export interface SessionSidebarProps {
  /** Currently active session id from the URL. */
  activeId: string | undefined;
  sidebarWidth: number;
  sidebarView: 'recent' | 'projects';
  toggleSidebarView: () => void;
  showArchivedRecent: boolean;
  setShowArchivedRecent: (updater: (current: boolean) => boolean) => void;
  loadingRecentSessions: boolean;
  recentSessions: Session[];
  sidebarProjectGroups: SidebarProjectGroup[];
  /** Persist a new drag-and-drop order of the project groups (directories). */
  onReorderProjects: (orderedDirectories: string[]) => void;
  archivingSessionIds: Set<string>;
  collapsedProjectSet: Set<string>;
  toggleCollapsedProject: (dir: string) => void;
  siblingGitInfos: Record<string, GitInfo>;
  optimisticStatus: Session['status'];
  debugMode: boolean;
  pendingTmuxSession: string | null;
  pickerPos: { top: number; left: number } | null;
  pickerRef: React.RefObject<HTMLDivElement | null>;
  tmux: TmuxState;
  onNavigateToSession: (id: string) => void;
  onArchiveSession: (e: React.MouseEvent, session: Session) => void;
  onPinSession: (e: React.MouseEvent, session: Session) => void;
  onClientSelect: (tty: string) => void;
  onNewSessionInDirectory: (directory: string) => void;
  onArchiveProject: (directory: string) => void;
}

/**
 * The full left sidebar: header buttons, tmux client picker, session list
 * (flat recent view or projects grouped view), and the backend stats footer.
 */
export function SessionSidebar({
  activeId,
  sidebarWidth,
  sidebarView,
  toggleSidebarView,
  showArchivedRecent,
  setShowArchivedRecent,
  loadingRecentSessions,
  recentSessions,
  sidebarProjectGroups,
  onReorderProjects,
  archivingSessionIds,
  collapsedProjectSet,
  toggleCollapsedProject,
  siblingGitInfos,
  optimisticStatus,
  debugMode,
  pendingTmuxSession,
  pickerPos,
  pickerRef,
  tmux,
  onNavigateToSession,
  onArchiveSession,
  onPinSession,
  onClientSelect,
  onNewSessionInDirectory,
  onArchiveProject,
}: SessionSidebarProps) {
  const sidebarListRef = useRef<HTMLDivElement>(null);

  // Keep the active session's sidebar row visible. The list doesn't reorder
  // to follow the cursor, so when the user switches sessions (or flips
  // views) the active row may be off-screen in a long list. We scroll it
  // into view with `nearest` block alignment so we don't yank the viewport
  // unless it's actually necessary. Skipped while the recent-sessions poll
  // is mid-flight for the initial load — the DOM may not yet contain a row
  // for `id`.
  useEffect(() => {
    if (!activeId) return;
    const container = sidebarListRef.current;
    if (!container) return;
    // Run on the next frame so any just-expanded group has finished laying
    // out before we measure offsets.
    const raf = requestAnimationFrame(() => {
      const active = container.querySelector('[aria-selected="true"]') as HTMLElement | null;
      if (!active) return;
      const cTop = container.scrollTop;
      const cBot = cTop + container.clientHeight;
      const aTop = active.offsetTop;
      const aBot = aTop + active.offsetHeight;
      if (aTop < cTop || aBot > cBot) {
        active.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
      }
    });
    return () => cancelAnimationFrame(raf);
  }, [activeId, sidebarView, recentSessions]);

  // Shared row renderer — used by both the flat and grouped views so
  // all live-status / archive / navigation behaviour stays identical.
  // For the currently-viewed session we trust the SSE-derived status
  // over the last poll (OpenCode's DB can lag SSE by several seconds;
  // using the poll value here would leave the sidebar pulse running
  // after the composer has already gone idle).
  const renderRow = (sib: Session, inGroup: boolean, depth = 0) => {
    const displayStatus = sib.id === activeId ? optimisticStatus : sib.status;
    // When a row sits inside a project group, surface the
    // worktree distinction (if any) next to the platform
    // badge so siblings stay distinguishable. The group
    // header already shows the project root; we only add a
    // hint when the session's actual cwd diverges from it
    // (i.e. it's a worktree, not the main checkout). For
    // those, the worktree slug — the final path segment of
    // <repo-parent>/.worktrees/<repo>/<slug> — is what the
    // user typed as the branch name in /wt, so we show that
    // alone with a small worktree icon. (Earlier versions
    // tried to slice the cwd by the project-root length,
    // but projectRoot isn't a prefix of cwd for worktrees,
    // so the result was a meaningless `.worktrees/<repo>/…`
    // string that got RTL-ellipsised into "trees/<repo>/…".)
    const projectRoot = projectRootForDirectory(sib.directory || '');
    const isWorktree = !!sib.directory && sib.directory !== projectRoot;
    return (
      <div
        key={sib.id}
        role="button"
        tabIndex={0}
        aria-selected={sib.id === activeId}
        className={`session-sidebar-item ${sib.id === activeId ? 'active' : ''}${archivingSessionIds.has(sib.id) ? ' archiving' : ''}${inGroup ? ' in-group' : ''}${depth > 0 ? ' session-sidebar-item-child' : ''}`}
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
        <StatusBadge
          status={displayStatus}
          compact
          seen={(displayStatus === 'waiting' || displayStatus === 'error' || displayStatus === 'done') && sib.seen}
          pending={sib.pendingPermission || sib.pendingQuestion}
          titleOverride={sib.notice?.message}
        />
        <span className="session-sidebar-item-body">
          <span className="session-sidebar-title">
            {cleanTitle(sib.title) || 'Untitled'}
          </span>
          {!inGroup && (
            <span className="session-sidebar-project">
              <PlatformBadge platform={sib.platform} variant="plain" />
              <span className="session-sidebar-project-path">
                <ShortPath path={isWorktree ? projectRoot : sib.directory} />
              </span>
            </span>
          )}
          {inGroup && (
            <span className="session-sidebar-project">
              <PlatformBadge platform={sib.platform} variant="plain" />
            </span>
          )}
          <GitStatusLine info={siblingGitInfos[sib.directory]} icon={isWorktree ? 'worktree' : 'branch'} />
        </span>
        <span className="session-sidebar-meta">
          <span className="session-sidebar-time" title={new Date(sib.timeUpdated).toLocaleString()}>{relativeTime(sib.timeUpdated)}</span>
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
              disabled={archivingSessionIds.has(sib.id)}
            >
              <ArchiveIcon />
            </button>
          </span>
        </span>
      </div>
    );
  };

  // The pinned group always renders first and is never reorderable;
  // the remaining project groups are drag-sortable.
  const pinnedGroup = sidebarProjectGroups.find((g) => g.isPinned);
  const sortableGroups = useMemo(
    () => sidebarProjectGroups.filter((g) => !g.isPinned),
    [sidebarProjectGroups],
  );

  const dndSensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 4 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 150, tolerance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const handleGroupDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event;
      if (!over || active.id === over.id) return;
      const dirs = sortableGroups.map((g) => g.directory);
      const from = dirs.indexOf(active.id as string);
      const to = dirs.indexOf(over.id as string);
      if (from === -1 || to === -1) return;
      onReorderProjects(arrayMove(dirs, from, to));
    },
    [sortableGroups, onReorderProjects],
  );

  const renderPinnedGroup = (group: SidebarProjectGroup) => {
    // The "Pinned" group is always expanded and has a
    // distinct header (pin icon, no collapse, no "+", not draggable).
    const agg = group.aggregate;
    const dotStatus =
      agg.kind === 'error' ? 'error'
        : agg.kind === 'busy' ? 'busy'
          : agg.kind === 'waiting' ? 'waiting'
            : 'done';
    const dotPending = agg.kind === 'pending';
    const dotSeen = agg.kind === 'none';
    return (
      <div key="__pinned__" className="session-sidebar-group session-sidebar-group-pinned">
        <div className="session-sidebar-group-header-row">
          <div className="session-sidebar-group-header" title="Pinned sessions">
            <span className="session-sidebar-group-status">
              <StatusBadge status={dotStatus} compact pending={dotPending} seen={dotSeen} />
            </span>
            <i className="bi bi-pin-fill session-sidebar-pinned-icon" aria-hidden="true" />
            <span className="session-sidebar-group-label">Pinned</span>
          </div>
        </div>
        {nestSessions(group.sessions).map(({ session: sib, depth }) => renderRow(sib, false, depth))}
      </div>
    );
  };

  // Header + session rows for a single (non-pinned) project group.
  // `dragHandle` is injected by SortableProjectGroup so the grip lives
  // in the header row but the drag listeners stay scoped to the handle
  // (the header button itself still toggles collapse on click).
  const renderGroupBody = (group: SidebarProjectGroup, dragHandle: React.ReactNode) => {
    const collapsed = collapsedProjectSet.has(group.directory);
    const label = group.directory ? shortPath(group.directory) : '(unknown)';
    // The status dot surfaces the rolled-up aggregate: the same visual
    // vocabulary as per-session rows (pending "!", error "!", busy
    // pulse, idle neutral), so a collapsed header tells you at a glance
    // which project needs attention. The header toggles collapse on
    // click; state is conveyed by `aria-expanded`.
    const agg = group.aggregate;
    const dotStatus =
      agg.kind === 'error' ? 'error'
        : agg.kind === 'busy' ? 'busy'
          : agg.kind === 'waiting' ? 'waiting'
            : 'done';
    const dotPending = agg.kind === 'pending';
    const dotSeen = agg.kind === 'none';
    const aggTitle =
      agg.kind === 'pending'
        ? `${agg.count} session${agg.count === 1 ? '' : 's'} waiting for your response`
        : agg.kind === 'error'
          ? `${agg.count} session${agg.count === 1 ? '' : 's'} with unseen errors`
          : agg.kind === 'busy'
            ? `${agg.count} running`
            : agg.kind === 'waiting'
              ? `${agg.count} unread`
              : `${group.sessions.length} session${group.sessions.length === 1 ? '' : 's'}`;
    return (
      <>
        <div className="session-sidebar-group-header-row">
          {dragHandle}
          <button
            type="button"
            className={`session-sidebar-group-header${collapsed ? ' collapsed' : ''}`}
            aria-expanded={!collapsed}
            title={group.directory || 'Unknown project'}
            onClick={() => toggleCollapsedProject(group.directory)}
          >
            <span className="session-sidebar-group-status" title={aggTitle}>
              <StatusBadge status={dotStatus} compact pending={dotPending} seen={dotSeen} />
            </span>
            <span className="session-sidebar-group-label">{label}</span>
          </button>
          {group.directory && (
            <button
              type="button"
              className="session-sidebar-group-new"
              onClick={(e) => {
                e.stopPropagation();
                void onNewSessionInDirectory(group.directory);
              }}
              title={`New session in ${label}`}
              aria-label={`New session in ${label}`}
            >+</button>
          )}
          {group.directory && (
            <button
              type="button"
              className="session-sidebar-group-new"
              onClick={(e) => {
                e.stopPropagation();
                onArchiveProject(group.directory);
              }}
              title={`Archive ${label}`}
              aria-label={`Archive ${label}`}
            ><ArchiveIcon /></button>
          )}
        </div>
        {!collapsed && nestSessions(group.sessions).map(({ session: sib, depth }) => renderRow(sib, true, depth))}
      </>
    );
  };

  const renderProjectsView = () => (
    <>
      {pinnedGroup && renderPinnedGroup(pinnedGroup)}
      <DndContext
        sensors={dndSensors}
        collisionDetection={closestCenter}
        onDragEnd={handleGroupDragEnd}
      >
        <SortableContext
          items={sortableGroups.map((g) => g.directory || '__empty__')}
          strategy={verticalListSortingStrategy}
        >
          {sortableGroups.map((group) => (
            <SortableProjectGroup
              key={group.directory || '__empty__'}
              id={group.directory || '__empty__'}
            >
              {(dragHandle) => renderGroupBody(group, dragHandle)}
            </SortableProjectGroup>
          ))}
        </SortableContext>
      </DndContext>
    </>
  );

  const renderFlatView = () => {
    // Flat view: pinned sessions at the top, then the rest.
    // Pinned sessions are deduplicated (shown only in the
    // pinned section, not repeated in the chronological list).
    const pinnedFlat = recentSessions
      .filter(s => s.pinned)
      .sort((a, b) => b.pinnedAt - a.pinnedAt);
    const unpinnedFlat = recentSessions.filter(s => !s.pinned);
    return (
      <>
        {nestSessions(pinnedFlat).map(({ session: sib, depth }) => renderRow(sib, false, depth))}
        {pinnedFlat.length > 0 && unpinnedFlat.length > 0 && (
          <div className="session-sidebar-divider" />
        )}
        {nestSessions(unpinnedFlat).map(({ session: sib, depth }) => renderRow(sib, false, depth))}
      </>
    );
  };

  return (
    <div className="session-sidebar" data-testid="session-sidebar" style={{ width: sidebarWidth }}>
      <SidebarResizer />
      <div className="session-sidebar-header">
        <span className="session-sidebar-heading" data-testid="sidebar-heading">
          <span>{sidebarView === 'projects' ? 'Projects' : 'Recent sessions'}</span>
        </span>
        <div className="session-sidebar-header-actions">
          <button
            type="button"
            className={`session-sidebar-new${sidebarView === 'projects' ? ' active' : ''}`}
            onClick={toggleSidebarView}
            title={sidebarView === 'projects' ? 'Show recent sessions' : 'Group by project'}
            aria-label={sidebarView === 'projects' ? 'Show recent sessions' : 'Group by project'}
          >{sidebarView === 'projects' ? <RecentViewIcon /> : <ProjectsViewIcon />}</button>
          <button
            type="button"
            className={`session-sidebar-new${showArchivedRecent ? ' active' : ''}`}
            onClick={() => {
              setShowArchivedRecent(current => !current);
            }}
            title={showArchivedRecent ? 'Hide archived sessions' : 'Include archived sessions'}
            aria-label={showArchivedRecent ? 'Hide archived sessions' : 'Include archived sessions'}
          ><ArchiveFilterIcon /></button>
        </div>
      </div>
      {pendingTmuxSession && pickerPos && (
        <div
          ref={pickerRef}
          className="tmux-client-popover"
          style={{ top: pickerPos.top, left: pickerPos.left }}
        >
          <div className="tmux-client-picker-header">
            <span>Select tmux client</span>
          </div>
          {tmux.clients.map(c => (
            <div
              key={c.tty}
              className="tmux-client-picker-item"
              onClick={() => onClientSelect(c.tty)}
            >
              <span className="tmux-client-tty">{c.tty}</span>
              <span className="tmux-client-session">{shortPath(c.session)}</span>
              <span className="tmux-client-size">{c.width}&times;{c.height}</span>
            </div>
          ))}
        </div>
      )}
      <div className="session-sidebar-list" ref={sidebarListRef}>
        {loadingRecentSessions ? (
          <SessionSidebarListSkeleton rows={5} />
        ) : recentSessions.length === 0 ? (
          <GettingStartedEmpty compact />
        ) : sidebarView === 'projects' ? (
          renderProjectsView()
        ) : (
          renderFlatView()
        )}
      </div>
      <BackendStats />
    </div>
  );
}

// SortableProjectGroup wraps one project group with dnd-kit's
// useSortable. It renders the group container and hands a drag-handle
// element to its render-prop child; the handle owns the drag listeners
// so the collapse-toggle button and "+" button stay clickable. A 4px
// activation distance lets a plain click through while still allowing
// a deliberate drag.
function SortableProjectGroup({
  id,
  children,
}: {
  id: string;
  children: (dragHandle: React.ReactNode) => React.ReactNode;
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id });
  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.4 : undefined,
  };
  const dragHandle = (
    <button
      type="button"
      className="session-sidebar-group-drag"
      title="Drag to reorder"
      aria-label="Drag to reorder project"
      {...attributes}
      {...listeners}
      onClick={(e) => e.stopPropagation()}
    >
      <i className="bi bi-grip-vertical" aria-hidden="true" />
    </button>
  );
  return (
    <div ref={setNodeRef} style={style} className="session-sidebar-group">
      {children(dragHandle)}
    </div>
  );
}
