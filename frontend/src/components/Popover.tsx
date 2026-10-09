import { useEffect, useEffectEvent, useRef, type CSSProperties, type ReactNode, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import './Popover.css';

/** Shared non-modal popover shell. Callers position it beside their trigger. */
export function Popover({ open, onClose, triggerRef, id, label, className = '', style, autoFocus = true, children }: {
  open: boolean;
  onClose: () => void;
  triggerRef: RefObject<HTMLElement | null>;
  id: string;
  label: string;
  className?: string;
  style?: CSSProperties;
  autoFocus?: boolean;
  children: ReactNode;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const close = useEffectEvent(onClose);
  useEffect(() => {
    if (!open) return;
    if (autoFocus) panel.current?.focus();
    const outside = (event: MouseEvent) => {
      if (!triggerRef.current?.contains(event.target as Node) && !panel.current?.contains(event.target as Node)) close();
    };
    const resize = () => close();
    const escape = (event: KeyboardEvent) => {
      // A nested picker handles its first Escape; the next closes the popover.
      if (event.key === 'Escape' && !panel.current?.querySelector('[role="combobox"][aria-expanded="true"]')) {
        event.stopPropagation();
        close();
        triggerRef.current?.focus();
      }
    };
    document.addEventListener('mousedown', outside);
    document.addEventListener('keydown', escape, true);
    window.addEventListener('resize', resize);
    return () => {
      document.removeEventListener('mousedown', outside);
      document.removeEventListener('keydown', escape, true);
      window.removeEventListener('resize', resize);
    };
  }, [open, triggerRef, autoFocus]);
  if (!open) return null;
  return createPortal(<div ref={panel} id={id} role="dialog" tabIndex={-1} aria-label={label}
    className={`oc-popover ${className}`.trim()} style={style}>{children}</div>, document.body);
}
