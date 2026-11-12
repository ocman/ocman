import type { ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import styles from './Drawer.module.css';

export function Drawer({ title, icon, description, onClose, canClose = true, closeLabel = `Close ${title}`, backdropTestId, children }: {
  title: string;
  icon?: ReactNode;
  description?: ReactNode;
  onClose: () => void;
  canClose?: boolean;
  closeLabel?: string;
  backdropTestId?: string;
  children: ReactNode;
}) {
  return createPortal(
    <Modal label={title} onClose={onClose} canClose={canClose} backdropTestId={backdropTestId} backdropClassName={styles.backdrop} dialogClassName={styles.drawer}>
      <ModalHeader title={title} icon={icon} description={description} onClose={onClose} canClose={canClose} closeLabel={closeLabel} />
      {children}
    </Modal>,
    document.body,
  );
}
