import type { ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import styles from './Drawer.module.css';

export function Drawer({ title, onClose, canClose = true, closeLabel = `Close ${title}`, backdropTestId, children }: {
  title: string;
  onClose: () => void;
  canClose?: boolean;
  closeLabel?: string;
  backdropTestId?: string;
  children: ReactNode;
}) {
  return createPortal(
    <Modal label={title} onClose={onClose} canClose={canClose} backdropTestId={backdropTestId} backdropClassName={styles.backdrop} dialogClassName={styles.drawer}>
      <ModalHeader title={title} onClose={onClose} canClose={canClose} closeLabel={closeLabel} />
      {children}
    </Modal>,
    document.body,
  );
}
