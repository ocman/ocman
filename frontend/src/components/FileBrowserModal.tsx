import type { ReactNode } from 'react';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import styles from './FileBrowserModal.module.css';

// Shadow-root rules supplement the library's host-level theme variables.
export const FILE_TREE_SELECTION_CSS = `
  button[data-type='item'][data-item-selected='true'] [data-item-section='content'] { color: var(--text); }
  button[data-type='item']:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
`;

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
