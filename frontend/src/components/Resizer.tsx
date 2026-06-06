import { useCallback, useEffect, useRef, useState } from 'react';
import './SidebarResizer.css';

// Amount the sidebar grows/shrinks per arrow-key press when the handle
// has keyboard focus.
const KEYBOARD_STEP = 16;

export interface ResizerProps {
  width: number;
  setWidth: (w: number) => void;
  min: number;
  max: number;
  defaultWidth: number;
  /**
   * When true, the handle sits on the LEFT edge of a right-anchored
   * panel: dragging left (negative delta) grows it, and the arrow keys
   * are inverted to match the visual direction. Also adds the
   * `mirrored` CSS class.
   */
  mirrored?: boolean;
  ariaLabel: string;
}

/**
 * Vertical pointer/keyboard drag handle for resizing a sidebar.
 *
 * Pointer-based so mouse, touch and pen all work. While dragging we set
 * a body-wide class that pins the cursor and blocks text selection; the
 * computed width is clamped + persisted by the supplied `setWidth`.
 *
 * Keyboard: when focused, arrows resize in KEYBOARD_STEP increments;
 * Home/End jump to min/max; double-click resets to `defaultWidth`. When
 * `mirrored`, the pointer delta and arrow-key direction are inverted so
 * the gesture matches a right-anchored panel.
 */
export function Resizer({ width, setWidth, min, max, defaultWidth, mirrored, ariaLabel }: ResizerProps) {
  const [dragging, setDragging] = useState(false);
  const startXRef = useRef(0);
  const startWidthRef = useRef(width);
  const sign = mirrored ? -1 : 1;

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
      setWidth(startWidthRef.current + sign * delta);
    },
    [dragging, setWidth, sign],
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
          setWidth(width - sign * KEYBOARD_STEP);
          break;
        case 'ArrowRight':
          e.preventDefault();
          setWidth(width + sign * KEYBOARD_STEP);
          break;
        case 'Home':
          e.preventDefault();
          setWidth(min);
          break;
        case 'End':
          e.preventDefault();
          setWidth(max);
          break;
      }
    },
    [setWidth, width, sign, min, max],
  );

  const onDoubleClick = useCallback(() => {
    setWidth(defaultWidth);
  }, [setWidth, defaultWidth]);

  return (
    <div
      className={`oc-sidebar-resizer${mirrored ? ' mirrored' : ''}${dragging ? ' dragging' : ''}`}
      role="separator"
      aria-orientation="vertical"
      aria-label={ariaLabel}
      aria-valuenow={width}
      aria-valuemin={min}
      aria-valuemax={max}
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
