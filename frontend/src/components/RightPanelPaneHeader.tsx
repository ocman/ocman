import { useEffect, useRef, useState } from 'react';
import type { ChangesSidebarTab } from '../lib/uiStore';
import { FullscreenButton } from './DiffFullscreenModal';
import type { PaneSummary } from './SessionChangesSidebar';
import { RefreshButton } from './RefreshButton';
import { MIN_PANE_FRACTION, TAB_ICONS, TAB_LABELS } from './rightPanelTabs';

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
export function PaneHeader({
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
          <RefreshButton onClick={onRefreshClick} loading={loading} />
        )}
      </span>
    </div>
  );
}
