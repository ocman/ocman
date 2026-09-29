import { useRef, useState } from 'react';
import { api } from '../../lib/api';
import { remoteLog } from '../../lib/remoteLog';
import { Modal } from '../../components/Modal';
import { Button, ButtonGroup, SubmitButton } from '../../components/Control';

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
  const submitRef = useRef<HTMLButtonElement>(null);

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
        onKeyDown={e => {
          if (e.key === 'Enter') submitRef.current?.click();
        }}
      />
      <div className="oc-rename-actions">
        <ButtonGroup label="Rename actions">
          <SubmitButton ref={submitRef} variant="accent" size="small" pendingLabel="Renaming…" onClick={handleSubmit}>
            Rename
          </SubmitButton>
          <Button size="small" onClick={onClose}>
            Cancel
          </Button>
        </ButtonGroup>
      </div>
    </Modal>
  );
}
