import { useCallback, useMemo, useState } from 'react';
import './SessionChangesSidebar.css';
import './RightPanel.css';
import { useUiStore, type ChangesSidebarTab } from '../lib/uiStore';
import { ChangesSidebarResizer } from './ChangesSidebarResizer';
import { useUpstreams } from '../lib/useUpstreams';
import { useProjectTarget } from '../lib/useProjectTarget';
import type { Session, SessionInfoCommit } from '../lib/api';
import type { MessageBookmark, MessageBookmarkGroup } from '../lib/messageBookmarks';
import { useBeadsStatus } from '../lib/useBeadsStatus';
import { Pane } from './RightPanelPane';
import { trackRender } from '../lib/renderRateMonitor';
import { TAB_ICONS, TAB_LABELS, normaliseSizes, reconcileTabOrder } from './rightPanelTabs';
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  MouseSensor,
  TouchSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from '@dnd-kit/core';
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';

interface RightPanelProps {
  sessionId: string;
  platformId: string | undefined;
  directory: string | undefined;
  // SSE-driven dirty tick passed through to both child hooks so an
  // edit event refreshes both panels in lockstep.
  dirtyTick?: number;
  // The currently-rendered session, threaded through so the
  // SessionInfoSidebar can display cross-platform metadata
  // (project, branch, status, message count, duration, lifetime
  // changes summary, total cost) without re-fetching it. Undefined
  // while the parent is still loading.
  session?: Session;
  messageBookmarkGroups: MessageBookmarkGroup[];
  selectedMessageBookmarkKey: string | null;
  onRemoveMessageBookmark: (bookmark: MessageBookmark) => void;
  onScrollToMessageBookmark: (bookmark: MessageBookmark) => void;
  onNavigateCommit?: (commit: SessionInfoCommit) => void;
  commitSourceStatus?: string | null;
}


// RightPanel renders the right-hand changes panel:
//   - Strip on the right edge with one icon per available view —
//     always visible. Icons can be drag-reordered via @dnd-kit.
//   - Content area to the left of the strip showing the currently-
//     open views, stacked vertically. Each pane header doubles as
//     a resize handle for the boundary above it.
//
// Click semantics on the strip:
//   - icon for a closed view  -> open it (appended to the bottom of
//                                the stack; if another view was
//                                open, this becomes a split).
//   - icon for the active view -> close it. If it was the only open
//                                 view, the panel collapses.
//   - drag an icon            -> reorder both the strip and the
//                                stacked panes.
//
// Designed to scale to N views: adding a third entry to
// DEFAULT_TAB_ORDER + a render branch is enough.
export function RightPanel({
  sessionId,
  platformId,
  directory,
  dirtyTick,
  session,
  messageBookmarkGroups,
  selectedMessageBookmarkKey,
  onRemoveMessageBookmark,
  onScrollToMessageBookmark,
  onNavigateCommit,
  commitSourceStatus,
}: RightPanelProps) {
  trackRender('RightPanel');
  const openTabs = useUiStore((s) => s.changesSidebarOpenTabs);
  const sizes = useUiStore((s) => s.changesSidebarTabSizes);
  const persistedOrder = useUiStore((s) => s.changesSidebarTabOrder);
  const toggleTab = useUiStore((s) => s.toggleChangesSidebarTab);
  const setTabSize = useUiStore((s) => s.setChangesSidebarTabSize);
  const setTabOrder = useUiStore((s) => s.setChangesSidebarTabOrder);
  // User-controlled width for the panel as a whole. The resizer lives
  // on the LEFT edge of the panel and writes back to the store.
  const width = useUiStore((s) => s.changesSidebarWidth);

  // Detect supported upstreams for the current project. The
  // 'upstream' pane is always available in the strip — when no
  // remote is detected the pane content explains why (e.g. "no
  // GitHub/Forgejo remote on this project"), keeping the feature
  // discoverable without cluttering projects that genuinely have
  // no upstream.
  const upstreamTarget = useProjectTarget(directory, session);
  const upstreamsResult = useUpstreams(openTabs.includes('upstream') ? upstreamTarget.directory : undefined, upstreamTarget.remoteId);
  const beadsResult = useBeadsStatus(
    directory,
    session ? session.remoteId || 'local' : undefined,
    openTabs.includes('beads'),
  );
  const beadsAvailable = beadsResult.data?.available === true;

  // Reconcile the persisted order against the known tab set: this
  // tolerates older persisted state that's missing newer tabs.
  const allTabOrder = useMemo(
    () => reconcileTabOrder(persistedOrder),
    [persistedOrder],
  );
  const stripOrder = useMemo(
    () => allTabOrder.filter((tab) => tab !== 'beads' || beadsAvailable),
    [allTabOrder, beadsAvailable],
  );

  // Render panes in the user-defined strip order (stripOrder),
  // filtered down to the panes currently open. Sorting follows the
  // strip so dragging an icon visually moves the corresponding pane
  // alongside it.
  const orderedOpenTabs = useMemo(
    () => stripOrder.filter((t) => openTabs.includes(t)),
    [stripOrder, openTabs],
  );
  const collapsed = orderedOpenTabs.length === 0;

  // Normalise the size fractions so they sum to 1 across the ordered
  // open tabs. Tabs without a stored size get an even share of
  // whatever's left after the explicit sizes are honoured.
  const normalisedSizes = useMemo(
    () => normaliseSizes(orderedOpenTabs, sizes),
    [orderedOpenTabs, sizes],
  );

  // Stable handler factory for the per-pair resize.
  const handlePaneResize = useCallback((idx: number, beforeSize: number, afterSize: number) => {
    setTabSize(orderedOpenTabs[idx], beforeSize);
    setTabSize(orderedOpenTabs[idx + 1], afterSize);
  }, [orderedOpenTabs, setTabSize]);

  // dnd-kit sensors: split Mouse + Touch sensors (instead of
  // PointerSensor) because PointerSensor on a <button> tends to
  // swallow the click that should toggle the tab — Mouse + Touch
  // give us cleaner coexistence. Activation distance of 4px is
  // enough to disambiguate a tap from a drag without feeling
  // sluggish.
  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 4 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 150, tolerance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  // Track the currently-dragged tab so the DragOverlay (portal-
  // rendered outside `.oc-changes-sidebar`'s `overflow: hidden`)
  // can show a floating preview that isn't clipped by the strip.
  const [draggingTab, setDraggingTab] = useState<ChangesSidebarTab | null>(null);

  const handleDragStart = useCallback((event: DragStartEvent) => {
    setDraggingTab(event.active.id as ChangesSidebarTab);
  }, []);

  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      setDraggingTab(null);
      const { active, over } = event;
      if (!over || active.id === over.id) return;
      const from = allTabOrder.indexOf(active.id as ChangesSidebarTab);
      const to = allTabOrder.indexOf(over.id as ChangesSidebarTab);
      if (from === -1 || to === -1) return;
      setTabOrder(arrayMove(allTabOrder, from, to));
    },
    [allTabOrder, setTabOrder],
  );

  const handleDragCancel = useCallback(() => {
    setDraggingTab(null);
  }, []);

  // Strip is rendered identically in every mode — it's the panel's
  // right edge. Active tabs are highlighted; clicking an active tab
  // closes it; dragging an icon reorders the strip + pane stack.
  // The DragOverlay renders the floating drag preview at the body
  // root so it escapes the sidebar's `overflow: hidden` clipping.
  const strip = (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      onDragCancel={handleDragCancel}
    >
      <SortableContext items={stripOrder} strategy={verticalListSortingStrategy}>
        <div className="oc-changes-strip" role="tablist" aria-label="Changes views">
          {stripOrder.map((t) => (
            <SortableStripIcon
              key={t}
              tab={t}
              active={orderedOpenTabs.includes(t)}
              onToggle={() => toggleTab(t)}
            />
          ))}
        </div>
      </SortableContext>
      <DragOverlay dropAnimation={null}>
        {draggingTab ? (
          <div
            className={`oc-changes-strip-icon active dragging-overlay`}
            aria-hidden="true"
          >
            <i className={`bi ${TAB_ICONS[draggingTab]}`} aria-hidden="true" />
          </div>
        ) : null}
      </DragOverlay>
    </DndContext>
  );

  if (collapsed) {
    return (
      <aside className="oc-changes-sidebar collapsed" aria-label="Changes (collapsed)">
        {strip}
      </aside>
    );
  }

  return (
    <aside
      className={`oc-changes-sidebar${orderedOpenTabs.length > 1 ? ' oc-right-panel-split' : ''}`}
      aria-label="Changes"
      style={{ width }}
    >
      <ChangesSidebarResizer />
      <div className="oc-changes-sidebar-content">
        {orderedOpenTabs.map((tab, idx) => (
          <Pane
            key={tab}
            tab={tab}
            sessionId={sessionId}
            platformId={platformId}
            directory={directory}
            dirtyTick={dirtyTick}
            session={session}
            messageBookmarkGroups={messageBookmarkGroups}
            selectedMessageBookmarkKey={selectedMessageBookmarkKey}
            onRemoveMessageBookmark={onRemoveMessageBookmark}
            onScrollToMessageBookmark={onScrollToMessageBookmark}
            onNavigateCommit={onNavigateCommit}
            commitSourceStatus={commitSourceStatus}
            upstreamTarget={upstreamTarget}
            upstreams={upstreamsResult.upstreams}
            upstreamLoading={upstreamsResult.loading}
            upstreamError={upstreamsResult.error}
            refreshUpstreams={upstreamsResult.refresh}
            beadsResult={beadsResult}
            // First pane has no top divider; subsequent panes do
            // and their header doubles as a resize handle for the
            // boundary above.
            divider={idx > 0}
            // Flex grow proportional to the size fraction. Multiplying
            // by 100 gives integer-ish values that flexbox handles
            // smoothly.
            size={normalisedSizes[idx]}
            resizeAboveIdx={idx > 0 ? idx - 1 : null}
            openTabs={orderedOpenTabs}
            sizes={normalisedSizes}
            onResize={handlePaneResize}
          />
        ))}
      </div>
      {strip}
    </aside>
  );
}

// SortableStripIcon wraps a single strip button with dnd-kit's
// useSortable hook. A 5px PointerSensor activation distance ensures
// a plain click still toggles the tab — dragging only kicks in once
// the user moves the pointer past that threshold.
function SortableStripIcon({
  tab,
  active,
  onToggle,
}: {
  tab: ChangesSidebarTab;
  active: boolean;
  onToggle: () => void;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: tab });
  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    // The DragOverlay renders the floating preview at the document
    // root; the in-place icon fades while the drag is active so the
    // user has a clear visual anchor for the source position.
    opacity: isDragging ? 0.3 : undefined,
  };
  // dnd-kit's `attributes` ships its own role ('button') for a11y
  // semantics on the drag handle. We override it back to 'tab'
  // *after* the spread so the strip stays a proper tablist; the
  // drag behaviour is unaffected.
  return (
    <button
      ref={setNodeRef}
      type="button"
      className={`oc-changes-strip-icon${active ? ' active' : ''}${isDragging ? ' dragging' : ''}`}
      onClick={onToggle}
      title={TAB_LABELS[tab]}
      style={style}
      {...attributes}
      {...listeners}
      role="tab"
      data-perf="panel-tab"
      aria-selected={active}
      aria-label={TAB_LABELS[tab]}
    >
      <i className={`bi ${TAB_ICONS[tab]}`} aria-hidden="true" />
    </button>
  );
}

