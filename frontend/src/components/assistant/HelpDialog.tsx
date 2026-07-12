import { useEffect } from 'react';
import { createPortal } from 'react-dom';
import type { SlashCommand } from '../../lib/api';
import '../CommandPalette.css';

export interface HelpDialogProps {
  open: boolean;
  commands: SlashCommand[];
  onClose: () => void;
}

/**
 * Minimal read-only dialog listing every available slash command. The
 * `commands` prop is the same merged set the slash menu uses
 * (BUILTIN_COMMANDS + live /api/slash-commands), so the dialog never
 * drifts from what the composer actually accepts.
 */
export function HelpDialog({ open, commands, onClose }: HelpDialogProps) {
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [open, onClose]);

  if (!open) return null;

  const sorted = [...commands].sort((a, b) => a.name.localeCompare(b.name));

  return createPortal(
    <div className="oc-cmd-backdrop" onClick={onClose}>
      <div className="oc-cmd-palette" onClick={(e) => e.stopPropagation()}>
        <div className="oc-cmd-input-wrap">
          <span className="oc-cmd-title">Slash commands</span>
          <kbd className="oc-cmd-kbd">ESC</kbd>
        </div>
        <div className="oc-cmd-results">
          {sorted.map((cmd) => (
            <div key={cmd.name} className="oc-cmd-item oc-cmd-item--command">
              <div className="oc-cmd-item-content">
                <span className="oc-cmd-title">/{cmd.name}</span>
                {cmd.description ? (
                  <span className="oc-cmd-meta">{cmd.description}</span>
                ) : null}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>,
    document.body,
  );
}
