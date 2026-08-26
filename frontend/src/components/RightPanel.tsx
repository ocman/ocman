import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import './SessionChangesSidebar.css';
import './RightPanel.css';
import { useUiStore, type ChangesSidebarTab } from '../lib/uiStore';
import { ChangesSidebarResizer } from './ChangesSidebarResizer';
import { FullscreenButton } from './DiffFullscreenModal';
import { ChangesRefreshButton, SessionChangesSidebar, type PaneSummary } from './SessionChangesSidebar';
import { WorkingTreeChangesSidebar } from './WorkingTreeChangesSidebar';
import { SessionInfoSidebar } from './SessionInfoSidebar';
import { UpstreamPane } from './upstream/UpstreamPane';
import { ErrorBoundary } from './ErrorBoundary';
import { useUpstreams } from '../lib/useUpstreams';
import type { Session } from '../lib/api';
import type { MessageBookmark, MessageBookmarkGroup } from '../lib/messageBookmarks';
import { MessageBookmarksPane } from './MessageBookmarksPane';
import { BeadsPane } from './BeadsPane';
import { useBeadsStatus } from '../lib/useBeadsStatus';
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
}

const TAB_LABELS: Record<ChangesSidebarTab, string> = {
  info: 'Session info',
  session: 'Session changes',
  'working-tree': 'Working tree',
  bookmarks: 'Bookmarks',
  upstream: 'PRs & Issues',
  beads: 'Beads',
};

// Info = info-circle icon (context / MCP / LSP overview).
// Session = pencil icon. Working tree = git branch icon. Upstream =
// inbox icon (incoming PRs / issues from the upstream forge —
// Bootstrap Icons doesn't ship a pull-request glyph, so inbox is
// the closest semantic neighbour). Same icon family used everywhere
// else in the app.
const TAB_ICONS: Record<ChangesSidebarTab, string> = {
  info: 'bi-info-circle',
  session: 'bi-pencil-square',
  'working-tree': 'bi-git',
  bookmarks: 'bi-bookmarks',
  upstream: 'bi-inbox',
  beads: 'bi-diagram-3',
};

// Default strip order, used as a fallback when the persisted order
// is missing entries (e.g. a new tab was added in a later ocman
// version). The user's drag-reordered list lives in the ui store.
const DEFAULT_TAB_ORDER: ChangesSidebarTab[] = [
  'info',
  'session',
  'working-tree',
  'bookmarks',
  'upstream',
  'beads',
];

// Minimum height fraction a single pane is allowed to occupy. Stops
// the user dragging a pane down to zero (where it would become
// unrecoverable without keyboard support).
const MIN_PANE_FRACTION = 0.1;

// reconcileTabOrder returns a complete ordering of every known tab,
// starting from the user's persisted order and appending any tabs
// that have been introduced since (or removing any that no longer
// exist). The result always contains exactly DEFAULT_TAB_ORDER's
// entries, in the user's preferred sequence where specified.
function reconcileTabOrder(persisted: ChangesSidebarTab[]): ChangesSidebarTab[] {
  const known = new Set<ChangesSidebarTab>(DEFAULT_TAB_ORDER);
  const seen = new Set<ChangesSidebarTab>();
  const result: ChangesSidebarTab[] = [];
  for (const t of persisted) {
    if (known.has(t) && !seen.has(t)) {
      result.push(t);
      seen.add(t);
    }
  }
  for (const t of DEFAULT_TAB_ORDER) {
    if (!seen.has(t)) result.push(t);
  }
  return result;
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
}: RightPanelProps) {
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
  const remoteId = session?.remoteId || 'local';
  const upstreamsResult = useUpstreams(directory, remoteId);
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
            upstreams={upstreamsResult.upstreams}
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
      aria-selected={active}
      aria-label={TAB_LABELS[tab]}
    >
      <i className={`bi ${TAB_ICONS[tab]}`} aria-hidden="true" />
    </button>
  );
}

// normaliseSizes returns one fraction per openTab in order. Stored
// values are honoured when present (after clamping to MIN_PANE_FRACTION);
// remaining tabs get an even share of whatever's left so the result
// always sums to 1.
function normaliseSizes(
  openTabs: ChangesSidebarTab[],
  sizes: Partial<Record<ChangesSidebarTab, number>>,
): number[] {
  const n = openTabs.length;
  if (n === 0) return [];
  if (n === 1) return [1];

  const result: number[] = [];
  let assigned = 0;
  let unassignedCount = 0;
  for (const t of openTabs) {
    const v = sizes[t];
    if (typeof v === 'number' && v > 0) {
      const clamped = Math.max(MIN_PANE_FRACTION, v);
      result.push(clamped);
      assigned += clamped;
    } else {
      result.push(-1); // placeholder
      unassignedCount++;
    }
  }
  if (unassignedCount === 0) {
    // All explicit — rescale so they sum to 1.
    return result.map((x) => x / assigned);
  }
  const remainder = Math.max(0, 1 - assigned);
  const share = unassignedCount > 0 ? remainder / unassignedCount : 0;
  return result.map((x) => (x === -1 ? Math.max(MIN_PANE_FRACTION, share) : x));
}

interface PaneProps {
  tab: ChangesSidebarTab;
  sessionId: string;
  platformId: string | undefined;
  directory: string | undefined;
  dirtyTick?: number;
  // Forwarded to SessionInfoSidebar's Session section. Other panes
  // ignore it. Tokens and Todos for the same pane come from the
  // /api/session/{id}/info endpoint, not from props.
  session?: Session;
  messageBookmarkGroups: MessageBookmarkGroup[];
  selectedMessageBookmarkKey: string | null;
  onRemoveMessageBookmark: (bookmark: MessageBookmark) => void;
  onScrollToMessageBookmark: (bookmark: MessageBookmark) => void;
  // Upstream remotes for the current project. Only the 'upstream' pane
  // consumes this; the other panes ignore it. Resolved at RightPanel
  // level so we don't re-detect per pane.
  upstreams: import('../lib/upstreamApi').Upstream[];
  beadsResult: ReturnType<typeof useBeadsStatus>;
  divider: boolean;
  size: number;
  // When non-null, the pane header doubles as a resize handle for
  // the boundary between pane[resizeAboveIdx] and this pane.
  resizeAboveIdx: number | null;
  openTabs: ChangesSidebarTab[];
  sizes: number[];
  onResize: (idx: number, beforeSize: number, afterSize: number) => void;
}

function Pane({
  tab,
  sessionId,
  platformId,
  directory,
  dirtyTick,
  session,
  messageBookmarkGroups,
  selectedMessageBookmarkKey,
  onRemoveMessageBookmark,
  onScrollToMessageBookmark,
  upstreams,
  beadsResult,
  divider,
  size,
  resizeAboveIdx,
  openTabs,
  sizes,
  onResize,
}: PaneProps) {
  // Children push their summary up via onSummaryChange so we can
  // render it next to the title without coupling RightPanel to
  // each view's data hook.
  const [summary, setSummary] = useState<PaneSummary>({ files: 0, additions: 0, deletions: 0 });
  // useCallback keeps the function reference stable across renders
  // so the child's effect doesn't loop (it depends on
  // onSummaryChange identity).
  const handleSummary = useCallback((s: PaneSummary) => setSummary(s), []);

  // Refresh callback exposed by the embedded sidebar. Held in a ref
  // so re-renders don't reset it; surfaced through state only so the
  // refresh button knows when the callback is actually wired up.
  const refreshRef = useRef<(() => void) | null>(null);
  const [hasRefresh, setHasRefresh] = useState(false);
  const handleRefresh = useCallback((fn: () => void) => {
    refreshRef.current = fn;
    setHasRefresh(true);
  }, []);
  const onRefreshClick = useCallback(() => {
    refreshRef.current?.();
  }, []);
  // Mirror the embedded sidebar's loading flag so the refresh button
  // in the pane header can spin its icon while a request is in flight.
  const [loading, setLoading] = useState(false);
  const handleLoadingChange = useCallback((next: boolean) => setLoading(next), []);

  // Same ref+flag dance as refresh, for the pane's fullscreen diff
  // browser. Only the diff panes wire it up; the rest never set the
  // flag so no button appears.
  const fullscreenRef = useRef<(() => void) | null>(null);
  const [hasFullscreen, setHasFullscreen] = useState(false);
  const handleFullscreen = useCallback((fn: () => void) => {
    fullscreenRef.current = fn;
    setHasFullscreen(true);
  }, []);
  const onFullscreenClick = useCallback(() => {
    fullscreenRef.current?.();
  }, []);

  return (
    <>
      <PaneHeader
        tab={tab}
        divider={divider}
        summary={summary}
        hasRefresh={hasRefresh}
        loading={loading}
        onRefreshClick={onRefreshClick}
        hasFullscreen={hasFullscreen}
        onFullscreenClick={onFullscreenClick}
        resizeAboveIdx={resizeAboveIdx}
        openTabs={openTabs}
        sizes={sizes}
        onResize={onResize}
      />
      <div className="oc-right-panel-pane" style={{ flexGrow: size, flexBasis: 0 }}>
        {/* Each pane gets its own boundary so a crash in one (bad diff
            payload, broken markdown, etc.) doesn't take the other panes
            down. resetKey on sessionId clears stale crashes when the user
            switches sessions. */}
        <ErrorBoundary name={`right-panel:${tab}`} inline resetKey={sessionId}>
          {tab === 'info' && (
            <SessionInfoSidebar
              sessionId={sessionId}
              platformId={platformId}
              dirtyTick={dirtyTick}
              session={session}
              embedded
              onSummaryChange={handleSummary}
              onRefresh={handleRefresh}
              onLoadingChange={handleLoadingChange}
            />
          )}
          {tab === 'session' && (
            <SessionChangesSidebar
              sessionId={sessionId}
              platformId={platformId}
              dirtyTick={dirtyTick}
              embedded
              onSummaryChange={handleSummary}
              onRefresh={handleRefresh}
              onLoadingChange={handleLoadingChange}
              onFullscreen={handleFullscreen}
            />
          )}
          {tab === 'working-tree' && (
            <WorkingTreeChangesSidebar
              directory={directory}
              dirtyTick={dirtyTick}
              embedded
              onSummaryChange={handleSummary}
              onRefresh={handleRefresh}
              onLoadingChange={handleLoadingChange}
              onFullscreen={handleFullscreen}
            />
          )}
          {tab === 'bookmarks' && (
            <MessageBookmarksPane
              groups={messageBookmarkGroups}
              selectedKey={selectedMessageBookmarkKey}
              onRemove={onRemoveMessageBookmark}
              onScrollToMessage={onScrollToMessageBookmark}
            />
          )}
          {tab === 'upstream' && (
            <UpstreamPane
              directory={directory}
              remoteId={session?.remoteId || 'local'}
              upstreams={upstreams}
              embedded
              onSummaryChange={handleSummary}
              onRefresh={handleRefresh}
              onLoadingChange={handleLoadingChange}
            />
          )}
          {tab === 'beads' && beadsResult.data?.available && (
            <BeadsPane
              status={beadsResult.data}
              loading={beadsResult.isFetching}
              error={beadsResult.error}
              refresh={beadsResult.refetch}
              onRefresh={handleRefresh}
              onLoadingChange={handleLoadingChange}
            />
          )}
        </ErrorBoundary>
      </div>
    </>
  );
}

interface PaneHeaderProps {
  tab: ChangesSidebarTab;
  divider: boolean;
  summary: PaneSummary;
  hasRefresh: boolean;
  loading: boolean;
  onRefreshClick: () => void;
  hasFullscreen: boolean;
  onFullscreenClick: () => void;
  // When non-null, this header doubles as a resize handle for the
  // boundary between pane[resizeAboveIdx] and the current pane.
  resizeAboveIdx: number | null;
  openTabs: ChangesSidebarTab[];
  sizes: number[];
  onResize: (idx: number, beforeSize: number, afterSize: number) => void;
}

// PaneHeader renders the title / summary / refresh row at the top
// of each open pane. When `resizeAboveIdx` is set (i.e. this is the
// 2nd+ pane in the stack), the header is wired up as a vertical
// drag handle that resizes the boundary above it. Buttons inside
// the header still receive clicks normally — the drag only kicks
// in when the user mouse-downs on the header background.
function PaneHeader({
  tab,
  divider,
  summary,
  hasRefresh,
  loading,
  onRefreshClick,
  hasFullscreen,
  onFullscreenClick,
  resizeAboveIdx,
  openTabs,
  sizes,
  onResize,
}: PaneHeaderProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [dragging, setDragging] = useState(false);
  const isResizable = resizeAboveIdx !== null;

  // Capture pointer-event start data so the drag move math can run
  // off the stored baseline rather than re-reading flex children
  // on every move (which would compound rounding error).
  const startRef = useRef<{
    startY: number;
    startBefore: number;
    startAfter: number;
    containerHeight: number;
  } | null>(null);

  useEffect(() => {
    if (!dragging) return;
    document.body.classList.add('oc-sidebar-resizing');

    const onMove = (e: PointerEvent) => {
      const s = startRef.current;
      if (!s || resizeAboveIdx === null) return;
      const pairSpan = s.startBefore + s.startAfter;
      const deltaPx = e.clientY - s.startY;
      const deltaFrac = s.containerHeight > 0 ? deltaPx / s.containerHeight : 0;
      const minBefore = MIN_PANE_FRACTION;
      const maxBefore = pairSpan - MIN_PANE_FRACTION;
      const newBefore = Math.max(minBefore, Math.min(maxBefore, s.startBefore + deltaFrac));
      const newAfter = pairSpan - newBefore;
      onResize(resizeAboveIdx, newBefore, newAfter);
    };

    const onUp = () => setDragging(false);
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onUp);
    return () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onUp);
      document.body.classList.remove('oc-sidebar-resizing');
    };
  }, [dragging, resizeAboveIdx, onResize]);

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!isResizable || resizeAboveIdx === null) return;
    // Skip drags that originate from interactive children (refresh
    // button, future menu buttons, etc.). The header background
    // itself initiates the drag.
    const target = e.target as HTMLElement;
    if (target.closest('button, a, input, select, textarea')) return;
    const container = ref.current?.parentElement;
    if (!container) return;
    const rect = container.getBoundingClientRect();
    startRef.current = {
      startY: e.clientY,
      startBefore: sizes[resizeAboveIdx],
      startAfter: sizes[resizeAboveIdx + 1],
      containerHeight: rect.height,
    };
    e.preventDefault();
    setDragging(true);
  };

  // Keyboard resize: ArrowUp/Down nudges the boundary in 4% steps,
  // PageUp/PageDown in 10% steps. Only active when this header is a
  // resize handle.
  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (!isResizable || resizeAboveIdx === null) return;
    const step = e.key === 'PageUp' || e.key === 'PageDown' ? 0.1 : 0.04;
    let delta = 0;
    if (e.key === 'ArrowUp' || e.key === 'PageUp') delta = -step;
    else if (e.key === 'ArrowDown' || e.key === 'PageDown') delta = step;
    else return;
    e.preventDefault();
    const before = sizes[resizeAboveIdx];
    const after = sizes[resizeAboveIdx + 1];
    const pairSpan = before + after;
    const minBefore = MIN_PANE_FRACTION;
    const maxBefore = pairSpan - MIN_PANE_FRACTION;
    const newBefore = Math.max(minBefore, Math.min(maxBefore, before + delta));
    onResize(resizeAboveIdx, newBefore, pairSpan - newBefore);
  };

  // ariaValueNow describes the share of the resizable PAIR taken
  // by the pane above the handle, expressed as a 0–100 integer.
  const ariaValueNow = isResizable && resizeAboveIdx !== null
    ? Math.round((sizes[resizeAboveIdx] / (sizes[resizeAboveIdx] + sizes[resizeAboveIdx + 1])) * 100)
    : undefined;

  return (
    <div
      ref={ref}
      className={[
        'oc-right-panel-pane-header',
        divider ? 'divider' : '',
        isResizable ? 'resizable' : '',
        dragging ? 'dragging' : '',
      ].filter(Boolean).join(' ')}
      onPointerDown={isResizable ? onPointerDown : undefined}
      onKeyDown={isResizable ? onKeyDown : undefined}
      role={isResizable ? 'separator' : undefined}
      aria-orientation={isResizable ? 'horizontal' : undefined}
      aria-valuenow={ariaValueNow}
      aria-valuemin={isResizable ? Math.round(MIN_PANE_FRACTION * 100) : undefined}
      aria-valuemax={isResizable ? Math.round((1 - MIN_PANE_FRACTION) * 100) : undefined}
      aria-label={isResizable ? `Resize ${TAB_LABELS[openTabs[resizeAboveIdx]]} / ${TAB_LABELS[tab]}` : undefined}
      tabIndex={isResizable ? 0 : undefined}
    >
      <span className="oc-right-panel-pane-title">
        <i className={`bi ${TAB_ICONS[tab]}`} aria-hidden="true" />
        {TAB_LABELS[tab]}
      </span>
      <span className="oc-right-panel-pane-summary">
        {summary.files > 0 && (
          <>
            <span className="oc-pane-summary-files">
              {summary.files} {summary.files === 1 ? 'file' : 'files'}
            </span>
            <span className="oc-changes-add">+{summary.additions}</span>
            <span className="oc-changes-del">-{summary.deletions}</span>
          </>
        )}
      </span>
      <span className="oc-right-panel-pane-actions">
        {hasFullscreen && <FullscreenButton onClick={onFullscreenClick} disabled={summary.files === 0} />}
        {hasRefresh && (
          <ChangesRefreshButton onClick={onRefreshClick} loading={loading} />
        )}
      </span>
    </div>
  );
}
