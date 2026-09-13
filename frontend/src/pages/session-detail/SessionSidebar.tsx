import type React from 'react';
import { useRef, useEffect, useCallback, useMemo, useState } from 'react';
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
import { cleanTitle, fuzzyMatch, shortPath } from '../../lib/format';
import { HostBadge } from '../../components/HostBadge';
import { ProjectLabel } from '../../components/ProjectLabel';
import { GitStatusLine } from '../../components/SessionTable';
import { BackendStats } from '../../components/BackendStats';
import { SidebarResizer } from '../../components/SidebarResizer';
import { SessionSidebarListSkeleton } from '../../components/Skeleton';
import { GettingStartedEmpty } from '../../components/GettingStartedEmpty';
import { rollupGroupStatus } from '../../lib/sidebarHelpers';
import { nestSessions } from '../../lib/nestSessions';
import { useDraftSessionIds } from '../../lib/composerDraft';
import { ArchiveIcon } from './SidebarIcons';
import { SidebarSessionRow } from './SidebarSessionRow';
import { SidebarHeader } from './SidebarHeader';
import { TmuxClientPopover } from './TmuxClientPopover';
import type { TmuxState } from '../../lib/useTmux';
import type { GitInfo } from '../../lib/api';

export interface SidebarProjectGroup {
  directory: string;
  sessions: Session[];
  lastUpdated: number;
  aggregate: ReturnType<typeof rollupGroupStatus>;
  isPinned?: boolean;
  /**
   * Owning host, carried at group level so a remote project still shows
   * its host badge when it has no sessions in the poll window (e.g. the
   * remote is offline). Empty/'local' for the local machine.
   */
  remoteId?: string;
  remoteName?: string;
  /** Compound platform id (r-<remoteId>:<base>) for remote projects. */
  platform?: string;
}

export interface SessionSidebarProps {
  /** Currently active session id from the URL. */
  activeId: string | undefined;
  sidebarWidth: number;
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
  activeDisplayStatus: Session['status'];
  debugMode: boolean;
  pendingTmuxSession: string | null;
  pickerPos: { top: number; left: number } | null;
  pickerRef: React.RefObject<HTMLDivElement | null>;
  tmux: TmuxState;
  onNavigateToSession: (id: string) => void;
  onArchiveSession: (e: React.MouseEvent, session: Session) => void;
  onPinSession: (e: React.MouseEvent, session: Session) => void;
  onClientSelect: (tty: string) => void;
  onNewSessionInDirectory: (directory: string, remoteId?: string, platform?: string) => void;
  onArchiveProject: (directory: string, remoteId?: string) => void;
}

/**
 * The full left sidebar: header buttons, tmux client picker, session list
 * (flat recent view or projects grouped view), and the backend stats footer.
 */
export function SessionSidebar({
  activeId,
  sidebarWidth,
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
  activeDisplayStatus,
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
  const [showChildren, setShowChildren] = useState(true);
  const [searchQuery, setSearchQuery] = useState('');
  const draftSessionIds = useDraftSessionIds();

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
  }, [activeId, recentSessions]);

  // Shared row renderer — used by both the pinned and grouped views so
  // all live-status / archive / navigation behaviour stays identical.
  const renderRow = (sib: Session, inGroup: boolean, depth = 0) => (
    <SidebarSessionRow
      key={sib.id}
      session={sib}
      inGroup={inGroup}
      depth={depth}
      active={sib.id === activeId}
      activeId={activeId}
      activeDisplayStatus={activeDisplayStatus}
      archiving={archivingSessionIds.has(sib.id)}
      draft={draftSessionIds.has(sib.id)}
      debugMode={debugMode}
      gitInfo={siblingGitInfos[sib.directory]}
      onNavigateToSession={onNavigateToSession}
      onArchiveSession={onArchiveSession}
      onPinSession={onPinSession}
    />
  );

  // The pinned group always renders first and is never reorderable;
  // the remaining project groups are drag-sortable.
  const filteredProjectGroups = useMemo(() => {
    const query = searchQuery.trim();
    if (showChildren && !query) return sidebarProjectGroups;
    return sidebarProjectGroups.flatMap((group) => {
      const sessions = group.sessions.filter((session) =>
        (showChildren || !session.parentId) &&
        (!query || fuzzyMatch(query, cleanTitle(session.title))),
      );
      return query && sessions.length === 0 ? [] : [{ ...group, sessions }];
    });
  }, [sidebarProjectGroups, searchQuery, showChildren]);

  const pinnedGroup = filteredProjectGroups.find((g) => g.isPinned && g.sessions.length > 0);
  const sortableGroups = useMemo(
    () => filteredProjectGroups.filter((g) => !g.isPinned),
    [filteredProjectGroups],
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
      const dirs = sidebarProjectGroups.filter((group) => !group.isPinned).map((group) => group.directory);
      const from = dirs.indexOf(active.id as string);
      const to = dirs.indexOf(over.id as string);
      if (from === -1 || to === -1) return;
      onReorderProjects(arrayMove(dirs, from, to));
    },
    [sidebarProjectGroups, onReorderProjects],
  );

  const renderPinnedGroup = (group: SidebarProjectGroup) => {
    // The "Pinned" group is always expanded and has a
    // distinct header (pin icon, no collapse, no "+", not draggable).
    return (
      <div key="__pinned__" className="session-sidebar-group session-sidebar-group-pinned">
        <div className="session-sidebar-group-header-row">
          <div className="session-sidebar-group-header" title="Pinned sessions">
            <i className="bi bi-pin-fill session-sidebar-pinned-icon" aria-hidden="true" />
            <span className="session-sidebar-group-label">Pinned</span>
          </div>
        </div>
        {nestSessions(group.sessions).map(({ session: sib, depth }) => renderRow(sib, false, depth))}
      </div>
    );
  };

  // Sessions of one project group, sub-grouped by working directory:
  // the main checkout first, then each worktree (most recently active
  // first). Every directory gets one small sub-header carrying the
  // branch/worktree identity, so individual rows stay a single line.
  const renderDirGroups = (
    group: SidebarProjectGroup,
    hostRemoteId?: string,
    hostPlatform?: string,
  ) => {
    const byDir = new Map<string, Session[]>();
    for (const s of group.sessions) {
      const dir = s.directory || group.directory;
      const bucket = byDir.get(dir);
      if (bucket) bucket.push(s);
      else byDir.set(dir, [s]);
    }
    const latest = (dir: string) =>
      Math.max(...(byDir.get(dir) ?? []).map((s) => s.timeUpdated));
    const dirs = [...byDir.keys()].sort((a, b) => {
      const aMain = a === group.directory ? 0 : 1;
      const bMain = b === group.directory ? 0 : 1;
      if (aMain !== bMain) return aMain - bMain;
      return latest(b) - latest(a);
    });
    return dirs.map((dir) => {
      const isWorktree = dir !== group.directory;
      const info = siblingGitInfos[dir];
      // Worktree slug — the final path segment of
      // <repo-parent>/.worktrees/<repo>/<slug> — is what the user
      // typed as the branch name in /wt; fall back to it when git
      // info hasn't loaded yet.
      const slug = dir.split('/').filter(Boolean).pop() || dir;
      const showHeader = isWorktree || !!info?.branch;
      const dirSessions = byDir.get(dir) ?? [];
      const dirLabel = info?.branch ?? slug;
      return (
        <div key={dir} className="session-sidebar-dir-group">
          {showHeader && (
            <div className="session-sidebar-dir-header" title={dir}>
              <span className="session-sidebar-dir-label">
                {info?.branch ? (
                  <GitStatusLine info={info} icon={isWorktree ? 'worktree' : 'branch'} />
                ) : (
                  <span className="git-status">
                    <i className="bi bi-diagram-2 git-status-icon" aria-hidden="true" />
                    <span className="git-status-branch">{slug}</span>
                  </span>
                )}
              </span>
              <button
                type="button"
                className="session-sidebar-group-new"
                onClick={(e) => {
                  e.stopPropagation();
                  void onNewSessionInDirectory(dir, hostRemoteId, hostPlatform);
                }}
                title={`New session on ${dirLabel}`}
                aria-label={`New session on ${dirLabel}`}
              >+</button>
            </div>
          )}
          {nestSessions(dirSessions).map(({ session: sib, depth }) =>
            renderRow(sib, true, depth),
          )}
        </div>
      );
    });
  };

  // Header + session rows for a single (non-pinned) project group.
  // `dragHandle` is injected by SortableProjectGroup so the grip lives
  // in the header row but the drag listeners stay scoped to the handle
  // (the header button itself still toggles collapse on click).
  const renderGroupBody = (group: SidebarProjectGroup, dragHandle: React.ReactNode) => {
    const collapsed = collapsedProjectSet.has(group.directory);
    const label = group.directory ? shortPath(group.directory) : '(unknown)';
    const remoteSession = group.sessions.find((s) => s.remoteId && s.remoteId !== 'local');
    // Prefer per-session host identity; fall back to the group's own
    // (set for session-less remote projects, e.g. an offline remote).
    // For a local group `remoteSession` is undefined, so anchor the
    // platform on the group's own first session — otherwise "+" passes
    // an undefined platform and handleNewSessionInDirectory leaks the
    // currently-open session's (possibly remote) platform onto this
    // local project.
    const hostRemoteId = remoteSession?.remoteId ?? group.remoteId;
    const hostRemoteName = remoteSession?.remoteName ?? group.remoteName;
    const hostPlatform = remoteSession?.platform ?? group.platform ?? group.sessions[0]?.platform;
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
            <ProjectLabel className="session-sidebar-group-label" path={group.directory} />
          </button>
          <HostBadge remoteName={hostRemoteName} remoteId={hostRemoteId} stale={remoteSession?.stale} />
          {group.directory && (
            <button
              type="button"
              className="session-sidebar-group-new"
              onClick={(e) => {
                e.stopPropagation();
                void onNewSessionInDirectory(group.directory, hostRemoteId, hostPlatform);
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
                onArchiveProject(group.directory, hostRemoteId);
              }}
              title={`Archive ${label}`}
              aria-label={`Archive ${label}`}
            ><ArchiveIcon /></button>
          )}
        </div>
        {!collapsed && renderDirGroups(group, hostRemoteId, hostPlatform)}
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

  return (
    <div className="session-sidebar" data-testid="session-sidebar" style={{ width: sidebarWidth }}>
      <SidebarResizer />
      <SidebarHeader
        searchQuery={searchQuery}
        setSearchQuery={setSearchQuery}
        showArchivedRecent={showArchivedRecent}
        setShowArchivedRecent={setShowArchivedRecent}
        showChildren={showChildren}
        setShowChildren={setShowChildren}
      />
      {pendingTmuxSession && pickerPos && (
        <TmuxClientPopover pickerRef={pickerRef} pos={pickerPos} clients={tmux.clients} onSelect={onClientSelect} />
      )}
      <div className="session-sidebar-list" ref={sidebarListRef}>
        {loadingRecentSessions ? (
          <SessionSidebarListSkeleton rows={5} />
        ) : // The sidebar always groups by project: list every unarchived
        // project even with no sessions (e.g. after archiving the last
        // one), so only fall back to the empty state when there are no
        // groups either.
        sidebarProjectGroups.length === 0 ? (
          <GettingStartedEmpty compact />
        ) : (
          renderProjectsView()
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
