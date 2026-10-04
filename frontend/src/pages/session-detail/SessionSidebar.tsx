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
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import type { Session } from '../../lib/api';
import { cleanTitle, fuzzyMatch } from '../../lib/format';
import { BackendStats } from '../../components/BackendStats';
import { SidebarResizer } from '../../components/SidebarResizer';
import { SessionSidebarListSkeleton } from '../../components/Skeleton';
import { GettingStartedEmpty } from '../../components/GettingStartedEmpty';
import { rollupGroupStatus, visibleSidebarSessions } from '../../lib/sidebarHelpers';
import { nestSessions } from '../../lib/nestSessions';
import { useDraftSessionIds } from '../../lib/composerDraft';
import { useWorkEpics } from '../../lib/queries';
import { SidebarProjectGroup as ProjectGroup } from './SidebarProjectGroup';
import { SidebarSessionRow } from './SidebarSessionRow';
import { useSidebarReorder } from './useSidebarReorder';
import { SidebarHeader } from './SidebarHeader';
import { useSidebarFilter } from './useSidebarFilter';
import { TmuxClientPopover } from './TmuxClientPopover';
import type { TmuxState } from '../../lib/useTmux';
import type { GitInfo } from '../../lib/api';
import { checkoutKey } from '../../lib/projectIdentity';

export interface SidebarProjectGroup {
  key?: string;
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
  sidebarView: 'recent' | 'projects';
  setSidebarView: (view: 'recent' | 'projects') => void;
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
  onNewSession: () => void;
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
  sidebarView,
  setSidebarView,
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
  onNewSession,
  onClientSelect,
  onNewSessionInDirectory,
  onArchiveProject,
}: SessionSidebarProps) {
  const sidebarListRef = useRef<HTMLDivElement>(null);
  useSidebarReorder(sidebarListRef, sidebarView);
  const [showChildren, setShowChildren] = useSidebarFilter('children', true);
  const [showFactory, setShowFactory] = useSidebarFilter('factory', false);
  const [showRoutines, setShowRoutines] = useSidebarFilter('routines', false);
  const [searchQuery, setSearchQuery] = useState('');
  const draftSessionIds = useDraftSessionIds();
  const { data: workEpics } = useWorkEpics();
  // Sessions hidden by the Factory/routine filters, including descendants.
  const hiddenSessions = useMemo(() => {
    const all = [...recentSessions, ...sidebarProjectGroups.flatMap((group) => group.sessions)];
    const keys = new Set<string>();
    if (!showFactory) {
      for (const epic of workEpics ?? []) {
        for (const { session } of epic.attempts ?? []) keys.add(`${session.platform}\0${session.id}`);
      }
    }
    if (!showRoutines) {
      for (const session of all) if (session.routineId) keys.add(`${session.platform}\0${session.id}`);
    }
    const children = new Map<string, string[]>();
    for (const session of all) {
      if (!session.parentId) continue;
      const parent = `${session.platform}\0${session.parentId}`;
      const siblings = children.get(parent) ?? [];
      siblings.push(`${session.platform}\0${session.id}`);
      children.set(parent, siblings);
    }
    // Set iteration visits added descendants too; membership checks stop cycles.
    for (const key of keys) {
      for (const child of children.get(key) ?? []) {
        if (!keys.has(child)) keys.add(child);
      }
    }
    return keys;
  }, [workEpics, recentSessions, sidebarProjectGroups, showFactory, showRoutines]);

  // Reveal the selection on navigation or initial load. Live activity must
  // not repeatedly pull the user away from a manually scrolled position.
  useEffect(() => {
    if (!activeId || loadingRecentSessions) return;
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
  }, [activeId, sidebarView, loadingRecentSessions]);

  // Shared row renderer — used by both the pinned and grouped views so
  // all live-status / archive / navigation behaviour stays identical.
  const renderRow = (sib: Session, inGroup: boolean, depth = 0, flat = false) => (
    <SidebarSessionRow
      key={sib.id}
      session={sib}
      inGroup={inGroup}
      flat={flat}
      depth={depth}
      active={sib.id === activeId}
      activeId={activeId}
      activeDisplayStatus={activeDisplayStatus}
      archiving={archivingSessionIds.has(sib.id)}
      draft={draftSessionIds.has(sib.id)}
      debugMode={debugMode}
      gitInfo={siblingGitInfos[checkoutKey(sib.directory, sib.remoteId)] ?? siblingGitInfos[sib.directory]}
      onNavigateToSession={onNavigateToSession}
      onArchiveSession={onArchiveSession}
      onPinSession={onPinSession}
    />
  );

  // The pinned group always renders first and is never reorderable;
  // the remaining project groups are drag-sortable.
  const filteredProjectGroups = useMemo(() => {
    const query = searchQuery.trim();
    if (showChildren && hiddenSessions.size === 0 && !query) return sidebarProjectGroups;
    return sidebarProjectGroups.flatMap((group) => {
      const projectMatches = !!query && fuzzyMatch(query, group.directory);
      const sessions = group.sessions.filter((session) =>
        !hiddenSessions.has(`${session.platform}\0${session.id}`) &&
        (showChildren || !session.parentId) &&
        (!query || projectMatches || matchesSessionSearch(query, session, siblingGitInfos[checkoutKey(session.directory, session.remoteId)] ?? siblingGitInfos[session.directory])),
      );
      return query && !projectMatches && sessions.length === 0 ? [] : [{ ...group, sessions }];
    });
  }, [sidebarProjectGroups, searchQuery, showChildren, siblingGitInfos, hiddenSessions]);

  const filteredPinnedSessions = useMemo(() => {
    const query = searchQuery.trim();
    return recentSessions
      .filter((session) => session.pinned)
      .filter((session) =>
        !hiddenSessions.has(`${session.platform}\0${session.id}`) &&
        (showChildren || !session.parentId) &&
        (!query || matchesSessionSearch(query, session, siblingGitInfos[checkoutKey(session.directory, session.remoteId)] ?? siblingGitInfos[session.directory])),
      )
      .sort((a, b) => b.pinnedAt - a.pinnedAt);
  }, [recentSessions, searchQuery, showChildren, siblingGitInfos, hiddenSessions]);
  const sortableGroups = useMemo(
    () => filteredProjectGroups.filter((g) => !g.isPinned),
    [filteredProjectGroups],
  );
  const flatUnpinned = useMemo(() => {
    const query = searchQuery.trim();
    return recentSessions.filter((session) =>
      !session.pinned &&
      !hiddenSessions.has(`${session.platform}\0${session.id}`) &&
      (showChildren || !session.parentId) &&
      (!query || matchesSessionSearch(query, session, siblingGitInfos[checkoutKey(session.directory, session.remoteId)] ?? siblingGitInfos[session.directory])),
    );
  }, [recentSessions, searchQuery, showChildren, siblingGitInfos, hiddenSessions]);

  // Publish what is on screen, in order, so archiving picks the next
  // session among the rows the user can actually see.
  useEffect(() => {
    const sections = sidebarView === 'recent'
      ? [filteredPinnedSessions, flatUnpinned]
      : [
        filteredPinnedSessions,
        ...sortableGroups
          .filter((group) => !collapsedProjectSet.has(group.key ?? group.directory))
          .map((group) => group.sessions),
      ];
    visibleSidebarSessions.current = sections.flatMap((rows) => nestSessions(rows).map(({ session }) => session));
  }, [sidebarView, filteredPinnedSessions, flatUnpinned, sortableGroups, collapsedProjectSet]);
  useEffect(() => () => { visibleSidebarSessions.current = null; }, []);

  const dndSensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 4 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 150, tolerance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const handleGroupDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event;
      if (!over || active.id === over.id) return;
      const dirs = sidebarProjectGroups.filter((group) => !group.isPinned).map((group) => group.key ?? group.directory);
      const from = dirs.indexOf(active.id as string);
      const to = dirs.indexOf(over.id as string);
      if (from === -1 || to === -1) return;
      onReorderProjects(arrayMove(dirs, from, to));
    },
    [sidebarProjectGroups, onReorderProjects],
  );

  const renderPinnedRows = (sessions: Session[]) =>
    nestSessions(sessions).map(({ session, depth }) => renderRow(session, false, depth, true));

  const renderPinnedGroup = (sessions: Session[]) => {
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
        {renderPinnedRows(sessions)}
      </div>
    );
  };

  const renderProjectsView = () => (
    <>
      {filteredPinnedSessions.length > 0 && renderPinnedGroup(filteredPinnedSessions)}
      <DndContext
        sensors={dndSensors}
        collisionDetection={closestCenter}
        onDragEnd={handleGroupDragEnd}
      >
        <SortableContext
          items={sortableGroups.map((g) => g.key ?? (g.directory || '__empty__'))}
          strategy={verticalListSortingStrategy}
        >
          {sortableGroups.map((group) => (
            <ProjectGroup
              key={group.key ?? (group.directory || '__empty__')}
              group={group}
              collapsed={collapsedProjectSet.has(group.key ?? group.directory)}
              siblingGitInfos={siblingGitInfos}
              toggleCollapsedProject={toggleCollapsedProject}
              onNewSessionInDirectory={onNewSessionInDirectory}
              onArchiveProject={onArchiveProject}
              renderRow={renderRow}
            />
          ))}
        </SortableContext>
      </DndContext>
    </>
  );

  const renderFlatView = () => {
    const unpinned = flatUnpinned;
    return (
      <>
        {filteredPinnedSessions.length > 0 && (
          <div className="session-sidebar-flat-pinned">
            {renderPinnedRows(filteredPinnedSessions)}
          </div>
        )}
        {filteredPinnedSessions.length > 0 && unpinned.length > 0 && (
          <div className="session-sidebar-flat-divider" data-testid="flat-pinned-divider" aria-hidden="true" />
        )}
        {nestSessions(unpinned).map(({ session, depth }) => renderRow(session, false, depth, true))}
      </>
    );
  };

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
        showFactory={showFactory}
        setShowFactory={setShowFactory}
        showRoutines={showRoutines}
        setShowRoutines={setShowRoutines}
        sidebarView={sidebarView}
        setSidebarView={setSidebarView}
        onNewSession={onNewSession}
      />
      {pendingTmuxSession && pickerPos && (
        <TmuxClientPopover pickerRef={pickerRef} pos={pickerPos} clients={tmux.clients} onSelect={onClientSelect} />
      )}
      <div className="session-sidebar-list" ref={sidebarListRef}>
        {loadingRecentSessions ? (
          <SessionSidebarListSkeleton rows={5} />
        ) : sidebarView === 'recent' && recentSessions.length === 0 ? (
          <GettingStartedEmpty compact />
        ) : sidebarView === 'recent' ? (
          renderFlatView()
        ) : sidebarProjectGroups.length === 0 ? (
          <GettingStartedEmpty compact />
        ) : (
          renderProjectsView()
        )}
      </div>
      <BackendStats />
    </div>
  );
}

function matchesSessionSearch(query: string, session: Session, gitInfo?: GitInfo): boolean {
  return [cleanTitle(session.title), session.directory, gitInfo?.branch]
    .some((value) => !!value && fuzzyMatch(query, value));
}
