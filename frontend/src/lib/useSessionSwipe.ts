import { useRef, useState, type CSSProperties, type PointerEvent, type MouseEvent } from 'react';
import { useIsPhone } from './useIsPhone';

interface Options {
  disabled?: boolean;
  onArchive: (event: PointerEvent<HTMLElement>) => void;
  onSnooze: () => void;
}

/** Touch-only actions. The browser keeps vertical scrolling and pinch zoom. */
export function useSessionSwipe({ disabled, onArchive, onSnooze }: Options) {
  const phone = useIsPhone();
  const [offset, setOffset] = useState(0);
  const gesture = useRef<{ id: number; x: number; y: number; axis: 'pending' | 'horizontal' | 'vertical'; dx: number } | null>(null);
  const suppressClick = useRef(false);

  function cancel() {
    gesture.current = null;
    setOffset(0);
  }

  return {
    style: { '--session-swipe-offset': `${offset}px` } as CSSProperties,
    'data-swipe-direction': offset ? (offset > 0 ? 'right' : 'left') : undefined,
    'data-swipe-label': offset ? (offset > 0 ? 'Archive' : 'Snooze') : undefined,
    onPointerDown(event: PointerEvent<HTMLElement>) {
      suppressClick.current = false;
      if (!phone || disabled || event.pointerType !== 'touch' || event.isPrimary === false) return;
      if ((event.target as Element).closest('button, input, select, textarea, [role="menu"]')) return;
      gesture.current = { id: event.pointerId, x: event.clientX, y: event.clientY, axis: 'pending', dx: 0 };
    },
    onPointerMove(event: PointerEvent<HTMLElement>) {
      const current = gesture.current;
      if (!current || current.id !== event.pointerId || current.axis === 'vertical') return;
      const dx = event.clientX - current.x;
      const dy = event.clientY - current.y;
      if (current.axis === 'pending') {
        if (Math.max(Math.abs(dx), Math.abs(dy)) < 10) return;
        if (Math.abs(dy) >= Math.abs(dx)) { current.axis = 'vertical'; return; }
        if (Math.abs(dx) < Math.abs(dy) * 1.4) return;
        current.axis = 'horizontal';
        event.currentTarget.setPointerCapture(event.pointerId);
      }
      current.dx = dx;
      suppressClick.current = true;
      setOffset(Math.max(-112, Math.min(112, dx)));
    },
    onPointerUp(event: PointerEvent<HTMLElement>) {
      const current = gesture.current;
      if (!current || current.id !== event.pointerId) return;
      cancel();
      if (!phone || disabled || current.axis !== 'horizontal') return;
      event.preventDefault();
      event.stopPropagation();
      if (current.dx >= 72) onArchive(event);
      else if (current.dx <= -72) onSnooze();
    },
    onPointerCancel: cancel,
    onLostPointerCapture(event: PointerEvent<HTMLElement>) {
      // A child link's implicit touch capture is released when the row takes over.
      if (event.target === event.currentTarget) cancel();
    },
    onClickCapture(event: MouseEvent<HTMLElement>) {
      if (suppressClick.current && event.detail !== 0) {
        event.preventDefault();
        event.stopPropagation();
      }
      suppressClick.current = false;
    },
  };
}
