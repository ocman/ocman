import { useEffect } from 'react';
import type { ReactNode } from 'react';

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
  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if (event.key === 'Escape' && canClose) onClose();
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [canClose, onClose]);
  return (
    <div className={backdropClassName} data-testid={backdropTestId} onClick={() => canClose && onClose()}>
      <div
        className={dialogClassName}
        role="dialog"
        aria-modal="true"
        aria-label={label}
        data-testid={dialogTestId}
        onClick={(event) => event.stopPropagation()}
      >
        {children}
      </div>
    </div>
  );
}
