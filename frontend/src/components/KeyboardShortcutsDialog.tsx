import './KeyboardShortcutsDialog.css';

interface ShortcutItem {
  keys: string;
  description: string;
}

const shortcuts: ShortcutItem[] = [
  {
    keys: 'Alt+?',
    description: 'Open keyboard shortcuts',
  },
  {
    keys: 'Alt+Space',
    description: 'Open command palette',
  },
  {
    keys: 'Alt+T',
    description: 'Switch tmux for current session or project',
  },
  {
    keys: 'Alt+V',
    description: 'Open current session or project in VS Code',
  },
  {
    keys: 'Alt+C',
    description: 'Create new session in current project',
  },
  {
    keys: 'Alt+J',
    description: 'Go to next session',
  },
  {
    keys: 'Alt+K',
    description: 'Go to previous session',
  },
  {
    keys: 'Alt+↑',
    description: 'Scroll up half a page',
  },
  {
    keys: 'Alt+↓',
    description: 'Scroll down half a page',
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
