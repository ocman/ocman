import { useEffect, useRef } from 'react';
import type { ReactNode } from 'react';

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

export function Modal({
  children,
  onClose,
  canClose = true,
  label,
  backdropClassName,
  dialogClassName,
  backdropTestId,
  dialogTestId,
}: {
  children: ReactNode;
  onClose: () => void;
  canClose?: boolean;
  label: string;
  backdropClassName: string;
  dialogClassName: string;
  backdropTestId?: string;
  dialogTestId?: string;
}) {
  const backdropRef = useRef<HTMLDivElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if (event.key === 'Escape' && canClose) onClose();
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [canClose, onClose]);

  // Focus management, once per open:
  //  - move focus into the dialog (unless a child grabbed it already,
  //    e.g. via autoFocus),
  //  - mark everything behind the dialog `inert`, which is the platform's
  //    own way of removing background content from the tab order, the
  //    a11y tree and pointer events — no hand-rolled trap needed for it,
  //  - hand focus back to whatever opened the dialog on close.
  useEffect(() => {
    const backdrop = backdropRef.current;
    const dialog = dialogRef.current;
    if (!backdrop || !dialog) return;
    const opener = document.activeElement as HTMLElement | null;

    const inerted: HTMLElement[] = [];
    for (let node: HTMLElement | null = backdrop; node && node !== document.body; node = node.parentElement) {
      for (const sibling of Array.from(node.parentElement?.children ?? [])) {
        if (sibling === node || !(sibling instanceof HTMLElement)) continue;
        if (sibling.hasAttribute('inert')) continue;
        sibling.setAttribute('inert', '');
        inerted.push(sibling);
      }
    }

    if (!dialog.contains(document.activeElement)) {
      (dialog.querySelector<HTMLElement>(FOCUSABLE) ?? dialog).focus();
    }

    return () => {
      // Un-inert before restoring focus: an inert element can't take focus.
      for (const node of inerted) node.removeAttribute('inert');
      if (opener?.isConnected) opener.focus();
    };
  }, []);

  // `inert` keeps Tab out of the background, but the browser would still
  // step from the last control into its own UI and back into the page.
  // Cycling within the dialog keeps a keyboard user inside it.
  function onKeyDown(event: React.KeyboardEvent) {
    if (event.key !== 'Tab') return;
    const dialog = dialogRef.current;
    if (!dialog) return;
    const items = Array.from(dialog.querySelectorAll<HTMLElement>(FOCUSABLE));
    if (items.length === 0) {
      event.preventDefault();
      dialog.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (event.shiftKey && (active === first || active === dialog)) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  return (
    <div
      ref={backdropRef}
      className={backdropClassName}
      data-testid={backdropTestId}
      onClick={() => canClose && onClose()}
      onKeyDown={onKeyDown}
    >
      <div
        ref={dialogRef}
        className={dialogClassName}
        role="dialog"
        aria-modal="true"
        aria-label={label}
        data-testid={dialogTestId}
        // Focus target when the dialog holds nothing focusable. Never
        // shows a ring: it is a container, not a control.
        tabIndex={-1}
        style={{ outline: 'none' }}
        onClick={(event) => event.stopPropagation()}
      >
        {children}
      </div>
    </div>
  );
}
