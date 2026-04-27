import { useCallback, useEffect, useRef, useState } from 'react';
import './SessionChangesSidebar.css';
import './RightPanel.css';
import { useUiStore, type ChangesSidebarTab } from '../lib/uiStore';
import { ChangesSidebarResizer } from './ChangesSidebarResizer';
import { ChangesRefreshButton, SessionChangesSidebar, type PaneSummary } from './SessionChangesSidebar';
import { WorkingTreeChangesSidebar } from './WorkingTreeChangesSidebar';
import { SessionInfoSidebar } from './SessionInfoSidebar';
import { ErrorBoundary } from './ErrorBoundary';
import type { Session } from '../lib/api';

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
}

const TAB_LABELS: Record<ChangesSidebarTab, string> = {
  info: 'Session info',
  session: 'Session changes',
  'working-tree': 'Working tree',
};

// Info = info-circle icon (context / MCP / LSP overview).
// Session = pencil icon. Working tree = git branch icon. Bootstrap
// Icons set, same family used everywhere else in the app.
const TAB_ICONS: Record<ChangesSidebarTab, string> = {
  info: 'bi-info-circle',
  session: 'bi-pencil-square',
  'working-tree': 'bi-git',
};

// Strip / pane order. Info sits above the two change-related panes
// (it's the higher-level "what is the session attached to" view),
// matching the OpenCode TUI layout.
const ALL_TABS: ChangesSidebarTab[] = ['info', 'session', 'working-tree'];

// Minimum height fraction a single pane is allowed to occupy. Stops
// the user dragging a pane down to zero (where it would become
// unrecoverable without keyboard support).
const MIN_PANE_FRACTION = 0.1;

// RightPanel renders the right-hand changes panel:
//   - Strip on the right edge with one icon per available view —
//     always visible.
//   - Content area to the left of the strip showing the currently-
//     open views, stacked vertically. Resizers sit between them.
//
// Click semantics on the strip:
//   - icon for a closed view  -> open it (appended to the bottom of
//                                the stack; if another view was
//                                open, this becomes a split).
//   - icon for the active view -> close it. If it was the only open
//                                 view, the panel collapses.
//
// Designed to scale to N views: adding a third entry to ALL_TABS +
// a render branch is enough.
export function RightPanel({ sessionId, platformId, directory, dirtyTick, session }: RightPanelProps) {
  const openTabs = useUiStore((s) => s.changesSidebarOpenTabs);
  const sizes = useUiStore((s) => s.changesSidebarTabSizes);
  const toggleTab = useUiStore((s) => s.toggleChangesSidebarTab);
  const setTabSize = useUiStore((s) => s.setChangesSidebarTabSize);
  // User-controlled width for the panel as a whole. The resizer lives
  // on the LEFT edge of the panel and writes back to the store.
  const width = useUiStore((s) => s.changesSidebarWidth);

  const collapsed = openTabs.length === 0;

  // Strip is rendered identically in every mode — it's the panel's
  // right edge. Active tabs are highlighted; clicking an active tab
  // closes it.
  const strip = (
    <div className="oc-changes-strip" role="tablist" aria-label="Changes views">
      {ALL_TABS.map((t) => {
        const isActive = openTabs.includes(t);
        return (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={isActive}
            className={`oc-changes-strip-icon${isActive ? ' active' : ''}`}
            onClick={() => toggleTab(t)}
            title={TAB_LABELS[t]}
            aria-label={TAB_LABELS[t]}
          >
            <i className={`bi ${TAB_ICONS[t]}`} aria-hidden="true" />
          </button>
        );
      })}
    </div>
  );

  if (collapsed) {
    return (
      <aside className="oc-changes-sidebar collapsed" aria-label="Changes (collapsed)">
        {strip}
      </aside>
    );
  }

  // Render panes in the canonical strip order (ALL_TABS) regardless
  // of the order the user clicked them. The store's openTabs is a
  // *set* of which tabs are visible; the layout is fixed by the
  // strip's icon order so users get a stable spatial mapping
  // (Session changes always above Working tree).
  const orderedOpenTabs = ALL_TABS.filter((t) => openTabs.includes(t));

  // Normalise the size fractions so they sum to 1 across the ordered
  // open tabs. Tabs without a stored size get an even share of
  // whatever's left after the explicit sizes are honoured.
  const normalisedSizes = normaliseSizes(orderedOpenTabs, sizes);

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
            // First pane has no top divider; subsequent panes do.
            divider={idx > 0}
            // Flex grow proportional to the size fraction. Multiplying
            // by 100 gives integer-ish values that flexbox handles
            // smoothly.
            size={normalisedSizes[idx]}
          />
        ))}
        {/* Vertical resize handles between adjacent panes. Each
            handle adjusts the pair of panes it sits between. */}
        {orderedOpenTabs.slice(0, -1).map((tab, idx) => (
          <PaneResizer
            key={`resizer-${tab}`}
            // Index of the pane *above* the handle, used to look up
            // the right pair to grow/shrink during a drag.
            beforeIdx={idx}
            openTabs={orderedOpenTabs}
            sizes={normalisedSizes}
            onUpdate={(beforeSize, afterSize) => {
              setTabSize(orderedOpenTabs[idx], beforeSize);
              setTabSize(orderedOpenTabs[idx + 1], afterSize);
            }}
          />
        ))}
      </div>
      {strip}
    </aside>
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
  divider: boolean;
  size: number;
}

function Pane({ tab, sessionId, platformId, directory, dirtyTick, session, divider, size }: PaneProps) {
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

  return (
    <>
      <div className={`oc-right-panel-pane-header${divider ? ' divider' : ''}`}>
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
          {hasRefresh && (
            <ChangesRefreshButton onClick={onRefreshClick} loading={loading} />
          )}
        </span>
      </div>
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
            />
          )}
        </ErrorBoundary>
      </div>
    </>
  );
}

interface PaneResizerProps {
  beforeIdx: number;
  openTabs: ChangesSidebarTab[];
  sizes: number[];
  onUpdate: (beforeSize: number, afterSize: number) => void;
}

// PaneResizer renders an absolutely-positioned drag handle between
// two adjacent panes in the split. Dragging adjusts the size fractions
// of just those two panes; the rest of the stack is unaffected.
//
// The handle is positioned dynamically via a ref because the panes
// themselves are flex children with grow/shrink — we measure the
// running offset from the top of the content area each render.
function PaneResizer({ beforeIdx, openTabs, sizes, onUpdate }: PaneResizerProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [dragging, setDragging] = useState(false);

  useEffect(() => {
    if (!dragging) return;
    document.body.classList.add('oc-sidebar-resizing');

    const onMove = (e: MouseEvent) => {
      const handle = ref.current;
      if (!handle) return;
      const container = handle.parentElement;
      if (!container) return;
      const rect = container.getBoundingClientRect();
      // Total span occupied by the two panes we control = their
      // combined fraction times container height.
      const beforeSize = sizes[beforeIdx];
      const afterSize = sizes[beforeIdx + 1];
      const pairSpan = (beforeSize + afterSize) * rect.height;

      // Top of the "before" pane in screen space — sum of all
      // earlier panes plus their header heights. Approximated as
      // proportional to size fractions; small inaccuracy from the
      // header bands is acceptable because the handle is ±1 px.
      let topFrac = 0;
      for (let i = 0; i < beforeIdx; i++) topFrac += sizes[i];
      const pairTop = rect.top + topFrac * rect.height;

      // Y position of the cursor, clamped to the legal range
      // (leaving each pane at least MIN_PANE_FRACTION of the pair).
      const minBefore = MIN_PANE_FRACTION * (beforeSize + afterSize);
      const maxBefore = (beforeSize + afterSize) - MIN_PANE_FRACTION * (beforeSize + afterSize);
      const offsetWithinPair = e.clientY - pairTop;
      const offsetFraction = pairSpan > 0 ? offsetWithinPair / rect.height : 0;
      const clamped = Math.max(minBefore, Math.min(maxBefore, offsetFraction));
      const newBefore = clamped;
      const newAfter = (beforeSize + afterSize) - clamped;
      onUpdate(newBefore, newAfter);
    };

    const onUp = () => setDragging(false);
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      document.body.classList.remove('oc-sidebar-resizing');
    };
  }, [dragging, beforeIdx, sizes, openTabs, onUpdate]);

  return (
    <div
      ref={ref}
      className={`oc-pane-resizer${dragging ? ' dragging' : ''}`}
      // Position the handle so it floats over the boundary between
      // pane[beforeIdx] and pane[beforeIdx+1]. We let CSS handle
      // ordering via flex (the resizer is absolutely positioned so
      // it doesn't take a flex slot).
      style={{ top: paneBoundaryPercent(beforeIdx, sizes) }}
      onMouseDown={(e) => {
        e.preventDefault();
        setDragging(true);
      }}
      role="separator"
      aria-orientation="horizontal"
    />
  );
}

// paneBoundaryPercent returns the CSS top-percent for the boundary
// after pane index `beforeIdx`. Used to position the absolute resizer
// handle. This is a quick approximation: it doesn't account for the
// per-pane header heights, so the handle may be a few pixels off the
// real boundary in edge cases. Acceptable because the handle has a
// tall hit area (8 px) and the visual position lines up after a few
// frames of grow/shrink.
function paneBoundaryPercent(beforeIdx: number, sizes: number[]): string {
  let frac = 0;
  for (let i = 0; i <= beforeIdx; i++) frac += sizes[i];
  return `${Math.min(99, Math.max(1, frac * 100))}%`;
}
