import { useState } from 'react';
import { api } from '../../lib/api';
import { remoteLog } from '../../lib/remoteLog';
import { Modal } from '../../components/Modal';

export interface RenameModalProps {
  sessionId: string;
  initialTitle: string;
  onClose: () => void;
  onRenamed: (newTitle: string) => void;
}

/**
 * Modal dialog for renaming a session. The caller is responsible for
 * mounting/unmounting based on a `showRenameModal` flag.
 */
export function RenameModal({ sessionId, initialTitle, onClose, onRenamed }: RenameModalProps) {
  const [renameTitle, setRenameTitle] = useState(initialTitle);

  const handleSubmit = async () => {
    try {
      await api.renameSession(sessionId, renameTitle.trim());
      onRenamed(renameTitle.trim());
      onClose();
    } catch (err) {
      remoteLog.error('Failed to rename session', err);
    }
  };

  return (
    <Modal
      backdropClassName="oc-rename-backdrop"
      dialogClassName="oc-rename-dialog"
      label="Rename Session"
      onClose={onClose}
    >
      <h3>Rename Session</h3>
      <input
        className="oc-rename-input"
        type="text"
        value={renameTitle}
        onChange={e => setRenameTitle(e.target.value)}
        placeholder="Session title"
        autoFocus
        onFocus={e => e.target.select()}
        onKeyDown={async e => {
          if (e.key === 'Enter') {
            await handleSubmit();
          }
        }}
      />
      <div className="oc-rename-actions">
        <button
          className="oc-rename-btn oc-rename-btn-submit"
          onClick={handleSubmit}
        >
          Rename
        </button>
        <button className="oc-rename-btn oc-rename-btn-cancel" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}
