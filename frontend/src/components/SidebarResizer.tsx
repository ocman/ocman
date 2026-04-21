import { useCallback, useEffect, useRef, useState } from 'react';
import {
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  useUiStore,
} from '../lib/uiStore';
import './SidebarResizer.css';

// Amount the sidebar grows/shrinks per arrow-key press when the handle
// has keyboard focus.
const KEYBOARD_STEP = 16;

/**
 * Vertical drag handle sitting on the right edge of `.session-sidebar`.
 *
 * Pointer-based so mouse, touch and pen all work. While dragging we set
 * a body-wide class that pins the cursor and blocks text selection; the
 * computed width is clamped and written to the ui store (which persists
 * it to localStorage via zustand/persist).
 *
 * Keyboard: when focused, Left/Right arrows resize in KEYBOARD_STEP
 * increments; Home/End jump to min/max; double-click resets to default.
 */
export function SidebarResizer() {
  const width = useUiStore((s) => s.sidebarWidth);
  const setWidth = useUiStore((s) => s.setSidebarWidth);
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
      // Capture so we keep getting move events even if the pointer
      // leaves the handle (e.g. during a fast drag).
      e.currentTarget.setPointerCapture(e.pointerId);
    },
    [width],
  );

  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (!dragging) return;
      const delta = e.clientX - startXRef.current;
      setWidth(startWidthRef.current + delta);
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
      switch (e.key) {
        case 'ArrowLeft':
          e.preventDefault();
          setWidth(width - KEYBOARD_STEP);
          break;
        case 'ArrowRight':
          e.preventDefault();
          setWidth(width + KEYBOARD_STEP);
          break;
        case 'Home':
          e.preventDefault();
          setWidth(SIDEBAR_MIN_WIDTH);
          break;
        case 'End':
          e.preventDefault();
          setWidth(SIDEBAR_MAX_WIDTH);
          break;
      }
    },
    [setWidth, width],
  );

  const onDoubleClick = useCallback(() => {
    setWidth(SIDEBAR_DEFAULT_WIDTH);
  }, [setWidth]);

  return (
    <div
      className={`oc-sidebar-resizer${dragging ? ' dragging' : ''}`}
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize sidebar"
      aria-valuenow={width}
      aria-valuemin={SIDEBAR_MIN_WIDTH}
      aria-valuemax={SIDEBAR_MAX_WIDTH}
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
