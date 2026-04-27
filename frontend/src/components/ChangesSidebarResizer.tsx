import { useCallback, useEffect, useRef, useState } from 'react';
import {
  CHANGES_SIDEBAR_DEFAULT_WIDTH,
  CHANGES_SIDEBAR_MAX_WIDTH,
  CHANGES_SIDEBAR_MIN_WIDTH,
  useUiStore,
} from '../lib/uiStore';
import './SidebarResizer.css';

// Amount the sidebar grows/shrinks per arrow-key press when the handle
// has keyboard focus.
const KEYBOARD_STEP = 16;

/**
 * Vertical drag handle sitting on the LEFT edge of `.oc-changes-sidebar`.
 *
 * Mirror of `SidebarResizer`: because the right sidebar is anchored to
 * the right edge of the layout, dragging the handle to the LEFT must
 * grow the sidebar — so the pointer delta is inverted relative to the
 * left sidebar's resizer. Otherwise the interaction model (pointer
 * capture, body class while dragging, keyboard arrows, double-click
 * reset) is identical.
 */
export function ChangesSidebarResizer() {
  const width = useUiStore((s) => s.changesSidebarWidth);
  const setWidth = useUiStore((s) => s.setChangesSidebarWidth);
  const [dragging, setDragging] = useState(false);
  const startXRef = useRef(0);
  const startWidthRef = useRef(width);

  const onPointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      // Ignore anything but primary button; still accept touch / pen.
      if (e.button !== 0 && e.pointerType === 'mouse') return;
      e.preventDefault();
      startXRef.current = e.clientX;
      startWidthRef.current = width;
      setDragging(true);
      e.currentTarget.setPointerCapture(e.pointerId);
    },
    [width],
  );

  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (!dragging) return;
      // Inverted: dragging left (negative delta) grows the sidebar.
      const delta = e.clientX - startXRef.current;
      setWidth(startWidthRef.current - delta);
    },
    [dragging, setWidth],
  );

  const endDrag = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragging) return;
    setDragging(false);
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      // ignore — capture may already be lost
    }
  }, [dragging]);

  // Toggle a body class while dragging so we can force the cursor and
  // kill text selection across the whole page (not just the handle).
  useEffect(() => {
    if (!dragging) return;
    document.body.classList.add('oc-sidebar-resizing');
    return () => {
      document.body.classList.remove('oc-sidebar-resizing');
    };
  }, [dragging]);

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLDivElement>) => {
      // Mirror keyboard semantics so they match visual direction:
      // ArrowLeft grows (handle moves left), ArrowRight shrinks.
      switch (e.key) {
        case 'ArrowLeft':
          e.preventDefault();
          setWidth(width + KEYBOARD_STEP);
          break;
        case 'ArrowRight':
          e.preventDefault();
          setWidth(width - KEYBOARD_STEP);
          break;
        case 'Home':
          e.preventDefault();
          setWidth(CHANGES_SIDEBAR_MIN_WIDTH);
          break;
        case 'End':
          e.preventDefault();
          setWidth(CHANGES_SIDEBAR_MAX_WIDTH);
          break;
      }
    },
    [setWidth, width],
  );

  const onDoubleClick = useCallback(() => {
    setWidth(CHANGES_SIDEBAR_DEFAULT_WIDTH);
  }, [setWidth]);

  return (
    <div
      className={`oc-sidebar-resizer mirrored${dragging ? ' dragging' : ''}`}
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize changes sidebar"
      aria-valuenow={width}
      aria-valuemin={CHANGES_SIDEBAR_MIN_WIDTH}
      aria-valuemax={CHANGES_SIDEBAR_MAX_WIDTH}
      tabIndex={0}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onKeyDown={onKeyDown}
      onDoubleClick={onDoubleClick}
      title="Drag to resize · double-click to reset"
    />
  );
}
