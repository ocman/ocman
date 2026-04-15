interface ShortcutItem {
  keys: string;
  description: string;
}

const shortcuts: ShortcutItem[] = [
  {
    keys: '?',
    description: 'Open keyboard shortcuts',
  },
  {
    keys: '/ or Ctrl+/',
    description: 'Open command palette',
  },
  {
    keys: 'T',
    description: 'Switch tmux for current session or project',
  },
  {
    keys: 'V',
    description: 'Open current session or project in VS Code',
  },
  {
    keys: 'N',
    description: 'Create new session in current project',
  },
  {
    keys: 'Ctrl+J',
    description: 'Jump to next user input in conversation history',
  },
  {
    keys: 'Ctrl+K',
    description: 'Jump to previous user input in conversation history',
  },
];

interface Props {
  open: boolean;
  onClose: () => void;
}

export function KeyboardShortcutsDialog({ open, onClose }: Props) {
  if (!open) return null;

  return (
    <div className="oc-shortcuts-backdrop" onClick={onClose}>
      <div className="oc-shortcuts-dialog" onClick={(e) => e.stopPropagation()} role="dialog" aria-modal="true" aria-label="Keyboard shortcuts">
        <div className="oc-shortcuts-header">
          <div>
            <h2>Keyboard shortcuts</h2>
            <p>Global shortcuts and current page actions.</p>
          </div>
          <button type="button" className="oc-shortcuts-close" onClick={onClose} aria-label="Close keyboard shortcuts">
            <i className="bi bi-x-lg" />
          </button>
        </div>
        <div className="oc-shortcuts-list">
          {shortcuts.map((shortcut) => (
            <div key={shortcut.keys} className="oc-shortcuts-item">
              <span>{shortcut.description}</span>
              <kbd className="oc-cmd-kbd">{shortcut.keys}</kbd>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
