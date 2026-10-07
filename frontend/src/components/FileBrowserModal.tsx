import type { ReactNode } from 'react';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import styles from './FileBrowserModal.module.css';

export function FileBrowserModal({ title, description, sidebar, children, onClose, dialogTestId }: {
  title: string;
  description?: ReactNode;
  sidebar: ReactNode;
  children: ReactNode;
  onClose: () => void;
  dialogTestId: string;
}) {
  return <Modal onClose={onClose} label={title} backdropClassName={styles.backdrop} dialogClassName={styles.modal} dialogTestId={dialogTestId}>
    <ModalHeader title={title} description={description} closeLabel="Close" onClose={onClose} className={styles.header} />
    <div className={styles.columns}>
      <div className={styles.files} data-testid="file-browser-sidebar">{sidebar}</div>
      <div className={styles.content} data-testid="file-browser-content">{children}</div>
    </div>
  </Modal>;
}
