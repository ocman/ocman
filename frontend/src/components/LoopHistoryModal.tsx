import './LoopEditModal.css';
import type { Loop } from '../lib/api.types';
import { LoopHistoryView } from './LoopHistoryView';
import { Modal } from './Modal';

interface LoopHistoryModalProps {
  loop: Loop;
  onClose: () => void;
}

/**
 * LoopHistoryModal shows a loop's history (the same LoopHistoryView used
 * inline in the sidebar) inside a dialog. Used by the /loops table, where
 * there's no room to expand the history inline.
 */
export function LoopHistoryModal({ loop, onClose }: LoopHistoryModalProps) {
  return (
    <Modal onClose={onClose} label="Loop history" backdropClassName="oc-loop-modal-backdrop" dialogClassName="oc-loop-modal oc-loop-history-modal" backdropTestId="loop-history-backdrop">
        <div className="oc-loop-modal-title">{loop.title || loop.id}</div>
        <LoopHistoryView loop={loop} />
        <div className="oc-loop-modal-actions">
          <button type="button" className="vscode-btn" onClick={onClose}>Close</button>
        </div>
    </Modal>
  );
}
