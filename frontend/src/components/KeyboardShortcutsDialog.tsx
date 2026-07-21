import './KeyboardShortcutsDialog.css';
import {
  bindingKeys,
  groupByScope,
  useShortcutRegistry,
  type KeyBinding,
  type Scope,
  type Shortcut,
} from '../lib/shortcutRegistry';
import { Modal } from './Modal';

interface Props {
  open: boolean;
  onClose: () => void;
}

// Display labels for each scope. Order matters only for the non-site
// scopes (which stack in the left column); `site` is rendered in its
// own right column regardless.
const SCOPE_LABELS: Partial<Record<Scope, string>> = {
  session: 'Session',
  project: 'Project',
  composer: 'Composer',
  prompt: 'Prompts',
};

// Non-site scopes, in the order they should stack in the left column.
const CONTEXT_SCOPES: Scope[] = ['session', 'project', 'composer', 'prompt'];

function KeyChips({ binding }: { binding: KeyBinding }) {
  const keys = bindingKeys(binding);
  return (
    <span className="oc-kbd-group">
      {keys.map((key, i) => (
        <kbd key={i} className="oc-kbd">{key}</kbd>
      ))}
    </span>
  );
}

function ShortcutSection({ title, shortcuts }: { title: string; shortcuts: Shortcut[] }) {
  if (shortcuts.length === 0) return null;
  return (
    <section className="oc-shortcuts-section">
      <h3 className="oc-shortcuts-section-title">{title}</h3>
      <div className="oc-shortcuts-list">
        {shortcuts.map((s) => {
          // Pick the primary binding for display. Multiple alternatives
          // (e.g. Alt+/ and Alt+Shift+/) collapse to the first one that
          // represents the "canonical" form — we rely on registrants listing
          // the preferred binding first.
          const bindings = Array.isArray(s.keys) ? s.keys : [s.keys];
          const primary = bindings[0];
          return (
            <div key={s.id} className="oc-shortcuts-item">
              <span>{s.description}</span>
              {primary && <KeyChips binding={primary} />}
            </div>
          );
        })}
      </div>
    </section>
  );
}

export function KeyboardShortcutsDialog({ open, onClose }: Props) {
  const shortcuts = useShortcutRegistry((s) => s.shortcuts);

  if (!open) return null;

  const groups = groupByScope(shortcuts.values());
  const hasAnyContextScope = CONTEXT_SCOPES.some((scope) => groups[scope].length > 0);

  return (
    <Modal
      backdropClassName="oc-shortcuts-backdrop"
      dialogClassName="oc-shortcuts-dialog"
      label="Keyboard shortcuts"
      onClose={onClose}
    >
      <div className="oc-shortcuts-header">
        <div>
          <h2>Keyboard shortcuts</h2>
          <p>Site-wide shortcuts and actions available on the current page.</p>
        </div>
        <button
          type="button"
          className="oc-shortcuts-close"
          onClick={onClose}
          aria-label="Close keyboard shortcuts"
        >
          <i className="bi bi-x-lg" />
        </button>
      </div>
      <div className={`oc-shortcuts-body${hasAnyContextScope ? '' : ' oc-shortcuts-body-single'}`}>
        {hasAnyContextScope && (
          <div className="oc-shortcuts-column">
            {CONTEXT_SCOPES.map((scope) => (
              <ShortcutSection
                key={scope}
                title={SCOPE_LABELS[scope] || scope}
                shortcuts={groups[scope]}
              />
            ))}
          </div>
        )}
        <div className="oc-shortcuts-column">
          <ShortcutSection title="Site-wide shortcuts" shortcuts={groups.site} />
        </div>
      </div>
    </Modal>
  );
}
