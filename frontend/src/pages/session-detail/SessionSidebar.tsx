import type React from 'react';
import { useRef, useEffect, useState } from 'react';
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
import { Resizer } from '../../components/Resizer';
import { SIDEBAR_DEFAULT_WIDTH, SIDEBAR_MAX_WIDTH, SIDEBAR_MIN_WIDTH, useUiStore } from '../../lib/uiStore';
import { SkeletonLines } from '../../components/Skeleton';
import { GettingStartedEmpty } from '../../components/GettingStartedEmpty';
import { rollupGroupStatus, visibleSidebarSessions } from '../../lib/sidebarHelpers';
import { nestSessions } from '../../lib/nestSessions';
import { useDraftSessionIds } from '../../lib/composerDraft';
import { SidebarProjectGroup as ProjectGroup } from './SidebarProjectGroup';
import { SidebarSessionRow } from './SidebarSessionRow';
import { useSidebarReorder } from './useSidebarReorder';
import { SidebarHeader } from './SidebarHeader';
import { useSessionListFilters } from '../../lib/useSessionListFilters';
import { hiddenSessionOrigins, matchesSessionListFilters } from '../../lib/sessionListFilters';
import { useSidebarProjectFilter } from './useSidebarProjectFilter';
import { TmuxClientPopover } from '../../components/TmuxClientPopover';
import type { TmuxState } from '../../lib/useTmux';
import type { GitInfo } from '../../lib/api';
import { checkoutKey } from '../../lib/projectIdentity';
import { countRender } from '../../lib/perfMonitor';
import { SidebarConversationDrafts } from './SidebarConversationDrafts';
import type { ConversationDraft } from '../../lib/newConversationDrafts';
import { useSidebarDraftGroups } from './useSidebarDraftGroups';
import { hasPendingPrompt } from '../../lib/sessionVisibility';
import { useIsPhone } from '../../lib/useIsPhone';
import { InlineAlert } from '../../components/InlineAlert';

export interface SidebarProjectGroup {
  key?: string;
  directory: string;
  sessions: Session[];
  drafts?: ConversationDraft[];
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
  visible?: boolean;
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
  archiveError?: string | null;
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

export function SessionSidebar({
  visible = true,
  activeId,
  sidebarWidth,
  sidebarView,
  setSidebarView,
  showArchivedRecent,
  setShowArchivedRecent,
  loadingRecentSessions,
  recentSessions: allRecentSessions,
  sidebarProjectGroups: allProjectGroups,
  onReorderProjects,
  archivingSessionIds,
  archiveError,
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
  countRender('SessionSidebar');
  const phone = useIsPhone();
  const setSidebarWidth = useUiStore((s) => s.setSidebarWidth);
  const [searchQuery, setSearchQuery] = useState('');
  const draftGroups = useSidebarDraftGroups(allProjectGroups, searchQuery);
  const { alwaysShowPrompts, setAlwaysShowPrompts, showChildren, setShowChildren, showFactory, setShowFactory,
    showRoutines, setShowRoutines, showSnoozed, setShowSnoozed } = useSessionListFilters();
  const { recentSessions, sidebarProjectGroups, projects, projectFilter, setProjectFilter } = useSidebarProjectFilter(allRecentSessions, draftGroups, alwaysShowPrompts);
  const preparedDrafts = sidebarProjectGroups.flatMap((group) => group.drafts ?? []);
  const sidebarListRef = useRef<HTMLDivElement>(null);
  useSidebarReorder(sidebarListRef, sidebarView);
  const draftSessionIds = useDraftSessionIds();
  // Sessions hidden by the Factory/routine filters, including descendants.
  const hiddenSessions = hiddenSessionOrigins(
    [...recentSessions, ...sidebarProjectGroups.flatMap((group) => group.sessions)], showFactory, showRoutines,
  );

  // Reveal the selection on navigation or initial load. Live activity must
  // not repeatedly pull the user away from a manually scrolled position.
  useEffect(() => {
    if (!activeId || loadingRecentSessions) return;
    const container = sidebarListRef.current;
    if (!container) return;
    // Wait for expanded groups to finish layout before measuring.
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

  // Share live status, archive and navigation across every session view.
  const renderRow = (sib: Session, inGroup: boolean, depth = 0, flat = false) => phone && archivingSessionIds.has(sib.id) ? null : (
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

  // Pinned rows come first; only project groups are reorderable.
  const query = searchQuery.trim();
  const filteredProjectGroups = sidebarProjectGroups.flatMap((group) => {
    const projectMatches = !!query && fuzzyMatch(query, group.directory);
    const sessions = group.sessions.filter((session) =>
      matchesSessionListFilters(session, { alwaysShowPrompts, showArchived: showArchivedRecent, showSnoozed, showChildren, showFactory, showRoutines },
        hiddenSessions, !query || projectMatches || matchesSessionSearch(query, session, siblingGitInfos[checkoutKey(session.directory, session.remoteId)] ?? siblingGitInfos[session.directory]), activeId),
    );
    return query && !projectMatches && sessions.length === 0 && !group.drafts?.length ? [] : [{ ...group, sessions }];
  });

  const filteredPinnedSessions = recentSessions
    .filter((session) => session.pinned && (!session.snoozedUntil || showSnoozed))
    .sort((a, b) => b.pinnedAt - a.pinnedAt);
  const sortableGroups = filteredProjectGroups.filter((g) => !g.isPinned);
  const flatUnpinned = recentSessions.filter((session) =>
    !session.pinned &&
    matchesSessionListFilters(session, { alwaysShowPrompts, showArchived: showArchivedRecent, showSnoozed, showChildren, showFactory, showRoutines },
      hiddenSessions, !query || matchesSessionSearch(query, session, siblingGitInfos[checkoutKey(session.directory, session.remoteId)] ?? siblingGitInfos[session.directory]), activeId),
  );

  // Publish what is on screen, in order, so archiving picks the next
  // session among the rows the user can actually see.
  useEffect(() => {
    const sections = sidebarView === 'recent'
      ? [filteredPinnedSessions, flatUnpinned]
      : [
        filteredPinnedSessions,
        ...sortableGroups
          .filter((group) => !collapsedProjectSet.has(group.key ?? group.directory) || alwaysShowPrompts && group.sessions.some(hasPendingPrompt))
          .map((group) => group.sessions),
      ];
    const seen = new Set<string>();
    visibleSidebarSessions.current = sections
      .flatMap((rows) => nestSessions(rows).map(({ session }) => session))
      .filter((session) => {
        const key = `${session.platform}\0${session.id}`;
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
  }, [sidebarView, filteredPinnedSessions, flatUnpinned, sortableGroups, collapsedProjectSet, alwaysShowPrompts]);
  useEffect(() => () => { visibleSidebarSessions.current = null; }, []);

  const dndSensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 4 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 150, tolerance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const handleGroupDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const dirs = draftGroups.filter((group) => !group.isPinned).map((group) => group.key ?? group.directory);
    const from = dirs.indexOf(active.id as string);
    const to = dirs.indexOf(over.id as string);
    if (from === -1 || to === -1) return;
    onReorderProjects(arrayMove(dirs, from, to));
  };

  const renderPinnedRows = (sessions: Session[]) =>
    nestSessions(sessions).map(({ session, depth }) => renderRow(session, false, depth, true));

  const renderPinnedGroup = (sessions: Session[]) => {
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
              collapsed={collapsedProjectSet.has(group.key ?? group.directory) && !(alwaysShowPrompts && group.sessions.some(hasPendingPrompt))}
              siblingGitInfos={siblingGitInfos}
              toggleCollapsedProject={toggleCollapsedProject}
              onNewSessionInDirectory={onNewSessionInDirectory}
              onArchiveProject={onArchiveProject}
              renderRow={renderRow}
              draftRows={<SidebarConversationDrafts drafts={group.drafts} searchQuery={searchQuery} inGroup />}
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
        {filteredPinnedSessions.length > 0 && (preparedDrafts.length > 0 || unpinned.length > 0) && (
          <div className="session-sidebar-flat-divider" data-testid="flat-pinned-divider" aria-hidden="true" />
        )}
        <SidebarConversationDrafts drafts={preparedDrafts} searchQuery={searchQuery} />
        {nestSessions(unpinned).map(({ session, depth }) => renderRow(session, false, depth, true))}
      </>
    );
  };

  return (
    <div className="session-sidebar" data-testid="session-sidebar" style={{ width: sidebarWidth }}>
      <Resizer
        width={sidebarWidth}
        setWidth={setSidebarWidth}
        min={SIDEBAR_MIN_WIDTH}
        max={SIDEBAR_MAX_WIDTH}
        defaultWidth={SIDEBAR_DEFAULT_WIDTH}
        ariaLabel="Resize sidebar"
      />
      <SidebarHeader
        projects={projects}
        projectFilter={projectFilter}
        setProjectFilter={setProjectFilter}
        searchQuery={searchQuery}
        setSearchQuery={setSearchQuery}
        showArchivedRecent={showArchivedRecent}
        setShowArchivedRecent={setShowArchivedRecent}
        showSnoozed={showSnoozed} setShowSnoozed={setShowSnoozed}
        showChildren={showChildren}
        setShowChildren={setShowChildren}
        showFactory={showFactory}
        setShowFactory={setShowFactory}
        showRoutines={showRoutines}
        setShowRoutines={setShowRoutines}
        alwaysShowPrompts={alwaysShowPrompts}
        setAlwaysShowPrompts={setAlwaysShowPrompts}
        sidebarView={sidebarView}
        setSidebarView={setSidebarView}
        onNewSession={onNewSession}
      />
      {archiveError && <InlineAlert>{archiveError}</InlineAlert>}
      {pendingTmuxSession && pickerPos && (
        <TmuxClientPopover pickerRef={pickerRef} pos={pickerPos} clients={tmux.clients} onSelect={onClientSelect} />
      )}
      <div className="session-sidebar-list" ref={sidebarListRef}>
        {loadingRecentSessions ? (
          <SkeletonLines rows={10} label="Loading sessions" />
        ) : sidebarView === 'recent' && allRecentSessions.length === 0 && !preparedDrafts.length ? (
          <GettingStartedEmpty compact />
        ) : sidebarView === 'recent' ? (
          renderFlatView()
        ) : draftGroups.length === 0 ? (
          <GettingStartedEmpty compact />
        ) : (
          renderProjectsView()
        )}
      </div>
      <BackendStats enabled={visible} />
    </div>
  );
}

function matchesSessionSearch(query: string, session: Session, gitInfo?: GitInfo): boolean {
  return [cleanTitle(session.title), session.directory, gitInfo?.branch ?? session.branch]
    .some((value) => !!value && fuzzyMatch(query, value));
}
