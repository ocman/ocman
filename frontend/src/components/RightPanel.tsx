import { useState } from 'react';
import './SessionChangesSidebar.css';
import './RightPanel.css';
import {
  CHANGES_SIDEBAR_DEFAULT_WIDTH,
  CHANGES_SIDEBAR_MAX_WIDTH,
  CHANGES_SIDEBAR_MIN_WIDTH,
  useUiStore,
  type ChangesSidebarTab,
} from '../lib/uiStore';
import { Resizer } from './Resizer';
import { useUpstreams } from '../lib/useUpstreams';
import { useProjectTarget } from '../lib/useProjectTarget';
import type { Session, SessionInfoCommit } from '../lib/api';
import type { MessageBookmark, MessageBookmarkGroup } from '../lib/messageBookmarks';
import { pluginPaneTab, usePluginPanes } from '../lib/pluginPanes';
import { Pane } from './RightPanelPane';
import { countRender } from '../lib/perfMonitor';
import { Button } from './Control';
import { PhoneSessionTools } from './PhoneSessionTools';
import { useIsPhone } from '../lib/useIsPhone';
import { EmptyState } from './EmptyState';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { TAB_ICONS, TAB_LABELS, mergeVisibleTabOrder, normaliseSizes, reconcileTabOrder } from './rightPanelTabs';
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

interface RightPanelProps {
  visible?: boolean;
  onCloseMobilePanel?: () => void;
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
  visible = true,
  onCloseMobilePanel,
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
  countRender('RightPanel');
  const phone = useIsPhone();
  const documentVisible = useDocumentVisible();
  const openTabs = useUiStore((s) => s.changesSidebarOpenTabs);
  const sizes = useUiStore((s) => s.changesSidebarTabSizes);
  const persistedOrder = useUiStore((s) => s.changesSidebarTabOrder);
  const toggleTab = useUiStore((s) => s.toggleChangesSidebarTab);
  const setTabSize = useUiStore((s) => s.setChangesSidebarTabSize);
  const setTabOrder = useUiStore((s) => s.setChangesSidebarTabOrder);
  // User-controlled width for the panel as a whole. The resizer lives
  // on the LEFT edge of the panel and writes back to the store.
  const width = useUiStore((s) => s.changesSidebarWidth);
  const setWidth = useUiStore((s) => s.setChangesSidebarWidth);

  // Detect supported upstreams for the current project. The
  // 'upstream' pane is always available in the strip — when no
  // remote is detected the pane content explains why (e.g. "no
  // GitHub/Forgejo remote on this project"), keeping the feature
  // discoverable without cluttering projects that genuinely have
  // no upstream.
  const upstreamTarget = useProjectTarget(directory, session);
  const upstreamsResult = useUpstreams(visible && openTabs.includes('upstream') ? upstreamTarget.directory : undefined, upstreamTarget.remoteId);
  const pluginPanesResult = usePluginPanes(visible && session ? session.remoteId || 'local' : undefined, documentVisible);
  const pluginPanes = pluginPanesResult.isError ? [] : pluginPanesResult.data ?? [];
  const tabLabels = { ...TAB_LABELS, ...Object.fromEntries(pluginPanes.map((pane) => [pluginPaneTab(pane), pane.pane.label])) };

  // Reconcile the persisted order against the known tab set: this
  // tolerates older persisted state that's missing newer tabs.
  const allTabOrder = reconcileTabOrder(persistedOrder, pluginPanes.map(pluginPaneTab));
  const stripOrder = allTabOrder;

  // Render panes in the user-defined strip order (stripOrder),
  // filtered down to the panes currently open. Sorting follows the
  // strip so dragging an icon visually moves the corresponding pane
  // alongside it.
  const stripOpenTabs = stripOrder.filter((t) => openTabs.includes(t));
  const orderedOpenTabs = phone ? stripOpenTabs.slice(0, 1) : stripOpenTabs;
  const collapsed = !phone && orderedOpenTabs.length === 0 && !pluginPanesResult.isError;
  const handleToggleTab = (tab: ChangesSidebarTab) => {
    if (!phone) { toggleTab(tab); return; }
    const closing = orderedOpenTabs.includes(tab);
    useUiStore.getState().setChangesSidebarOpenTabs(closing ? [] : [tab]);
    if (closing) onCloseMobilePanel?.();
  };

  // Normalise the size fractions so they sum to 1 across the ordered
  // open tabs. Tabs without a stored size get an even share of
  // whatever's left after the explicit sizes are honoured.
  const normalisedSizes = normaliseSizes(orderedOpenTabs, sizes);

  // Stable handler factory for the per-pair resize.
  const handlePaneResize = (idx: number, beforeSize: number, afterSize: number) => {
    setTabSize(orderedOpenTabs[idx], beforeSize);
    setTabSize(orderedOpenTabs[idx + 1], afterSize);
  };

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

  const handleDragStart = (event: DragStartEvent) => {
    setDraggingTab(event.active.id as ChangesSidebarTab);
  };

  const handleDragEnd = (event: DragEndEvent) => {
    setDraggingTab(null);
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const from = allTabOrder.indexOf(active.id as ChangesSidebarTab);
    const to = allTabOrder.indexOf(over.id as ChangesSidebarTab);
    if (from === -1 || to === -1) return;
    setTabOrder(mergeVisibleTabOrder(persistedOrder, arrayMove(allTabOrder, from, to)));
  };

  const handleDragCancel = () => {
    setDraggingTab(null);
  };

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
              label={tabLabels[t]}
              active={orderedOpenTabs.includes(t)}
              onToggle={() => handleToggleTab(t)}
            />
          ))}
          <div id="phone-terminal-slot" />
        </div>
      </SortableContext>
      <DragOverlay dropAnimation={null}>
        {draggingTab ? (
          <div
            className={`oc-changes-strip-icon active dragging-overlay`}
            aria-hidden="true"
          >
            <i className={`bi ${TAB_ICONS[draggingTab] ?? 'bi-diagram-3'}`} aria-hidden="true" />
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
      {/* Mirrored: the panel is anchored right, so dragging left grows it. */}
      <Resizer
        width={width}
        setWidth={setWidth}
        min={CHANGES_SIDEBAR_MIN_WIDTH}
        max={CHANGES_SIDEBAR_MAX_WIDTH}
        defaultWidth={CHANGES_SIDEBAR_DEFAULT_WIDTH}
        mirrored
        ariaLabel="Resize changes sidebar"
      />
      <div className="oc-changes-sidebar-content">
        {phone && <PhoneSessionTools key={sessionId} session={session} />}
        {pluginPanesResult.isError && (
          <EmptyState role="alert">
            Could not load plugin panes.
            <Button variant="ghost" onClick={() => void pluginPanesResult.refetch()}>Retry plugin panes</Button>
          </EmptyState>
        )}
        {visible && orderedOpenTabs.map((tab, idx) => (
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
            pluginPane={pluginPanes.find((pane) => pluginPaneTab(pane) === tab)}
            tabLabels={tabLabels}
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
  label,
  active,
  onToggle,
}: {
  tab: ChangesSidebarTab;
  label: string;
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
    transform: transform ? `translate3d(${transform.x ? Math.round(transform.x) : 0}px, ${transform.y ? Math.round(transform.y) : 0}px, 0) scaleX(${transform.scaleX}) scaleY(${transform.scaleY})` : undefined,
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
      title={label}
      style={style}
      {...attributes}
      {...listeners}
      role="tab"
      data-perf="panel-tab"
      aria-selected={active}
      aria-label={label}
    >
      <i className={`bi ${TAB_ICONS[tab] ?? 'bi-diagram-3'}`} aria-hidden="true" />
    </button>
  );
}
