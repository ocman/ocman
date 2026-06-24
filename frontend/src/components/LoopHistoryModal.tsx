import { useEffect } from 'react';
import './LoopEditModal.css';
import type { Loop } from '../lib/api.types';
import { LoopHistoryView } from './LoopHistoryView';

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
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  return (
    <div className="oc-loop-modal-backdrop" data-testid="loop-history-backdrop" onClick={onClose}>
      <div
        className="oc-loop-modal oc-loop-history-modal"
        role="dialog"
        aria-label="Loop history"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="oc-loop-modal-title">{loop.title || loop.id}</div>
        <LoopHistoryView loop={loop} />
        <div className="oc-loop-modal-actions">
          <button type="button" className="vscode-btn" onClick={onClose}>Close</button>
        </div>
      </div>
    </div>
  );
}
