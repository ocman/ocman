import { useEffect, useState } from 'react';
import { api, type PermissionRule } from '../lib/api';
import { classifyPermissionMode, PERMISSION_MODES } from '../lib/permissionModes';

/**
 * Session-header lock: shows the session's current permission posture
 * and lets the user switch between presets (default / plan-only /
 * auto-edit / yolo). Renders only when the caller has already gated
 * on caps.permissionRules and a live connection.
 */
export function PermissionModeLock({ sessionId }: { sessionId: string }) {
  const [rules, setRules] = useState<PermissionRule[] | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    setRules(null);
    setError('');
    api
      .getPermissionRules(sessionId)
      .then((resp) => {
        if (!cancelled) setRules(resp.rules);
      })
      .catch(() => {
        // No live instance / transient error: hide the control rather
        // than surface an error for a passive read.
        if (!cancelled) setRules(null);
      });
    return () => {
      cancelled = true;
    };
  }, [sessionId]);

  if (rules === null) return null;

  const modeId = classifyPermissionMode(rules);
  const mode = PERMISSION_MODES.find((m) => m.id === modeId);
  const label = mode ? mode.label : 'Custom';

  const apply = async (id: string) => {
    const target = PERMISSION_MODES.find((m) => m.id === id);
    if (!target || saving) return;
    if (
      target.dangerous &&
      !window.confirm(`Switch to ${target.label}? The agent will run edits and commands without asking.`)
    ) {
      return;
    }
    setSaving(true);
    setError('');
    try {
      await api.setPermissionRules(sessionId, target.rules);
      setRules(target.rules);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to set permission mode');
    } finally {
      setSaving(false);
    }
  };

  return (
    <details className="oc-project-menu header-actions-menu" data-testid="permission-mode-lock">
      <summary
        className={`oc-project-menu-trigger oc-permission-lock${modeId === 'yolo' ? ' dangerous' : ''}`}
        title={error || `Permission mode: ${label}`}
        aria-label={`Permission mode: ${label}`}
      >
        <svg width="12" height="12" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
          <path d="M4 7V5a4 4 0 1 1 8 0v2h.5A1.5 1.5 0 0 1 14 8.5v5A1.5 1.5 0 0 1 12.5 15h-9A1.5 1.5 0 0 1 2 13.5v-5A1.5 1.5 0 0 1 3.5 7H4zm1.5-2v2h5V5a2.5 2.5 0 0 0-5 0z" />
        </svg>
        <span className="oc-permission-lock-label">{label}</span>
      </summary>
      <div className="oc-project-menu-list" role="menu">
        {PERMISSION_MODES.map((m) => (
          <button
            key={m.id}
            type="button"
            role="menuitemradio"
            aria-checked={m.id === modeId}
            className="oc-project-menu-item"
            disabled={saving}
            onClick={(e) => {
              (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
              void apply(m.id);
            }}
            title={m.description}
          >
            {m.id === modeId ? '● ' : ''}
            {m.label}
          </button>
        ))}
        {error && <div className="oc-project-menu-item oc-permission-lock-error">{error}</div>}
      </div>
    </details>
  );
}
