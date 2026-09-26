import type { ReactNode } from 'react';
import { IconButton } from './IconButton';
import './ModalHeader.css';

interface Props {
  title: string;
  description?: ReactNode;
  onClose: () => void;
  closeLabel: string;
  canClose?: boolean;
  className?: string;
}

export function ModalHeader({ title, description, onClose, closeLabel, canClose = true, className = '' }: Props) {
  return (
    <header className={`oc-modal-header ${className}`} data-testid="modal-header">
      <div><h2>{title}</h2>{description && <p>{description}</p>}</div>
      <IconButton icon="bi-x-lg" label={closeLabel} title="Close" variant="ghost" disabled={!canClose} onClick={onClose} />
    </header>
  );
}
