import { useCallback, useEffect, useMemo, useState } from 'react';
import { api, type PermissionRule } from '../lib/api';
import { classifyPermissionMode, PERMISSION_MODES, type PermissionMode } from '../lib/permissionModes';
import { CommandListPicker } from './assistant/CommandListPicker';

const MENU_PERMISSION_MODES = PERMISSION_MODES;

/**
 * Session permission-mode lock: shows the current permission posture
 * and lets the user switch between presets. Renders only when the caller has already gated
 * on caps.permissionRules and a live connection.
 */
export function PermissionModeLock({ sessionId }: { sessionId: string }) {
  const [rules, setRules] = useState<PermissionRule[] | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [pickerOpen, setPickerOpen] = useState(false);
  const [confirmMode, setConfirmMode] = useState<PermissionMode | null>(null);

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

  useEffect(() => {
    const open = () => setPickerOpen(true);
    window.addEventListener('oc-permission-mode-open', open);
    return () => window.removeEventListener('oc-permission-mode-open', open);
  }, []);

  const modeId = rules === null ? '' : classifyPermissionMode(rules);
  const mode = PERMISSION_MODES.find((m) => m.id === modeId);
  const label = mode ? mode.label : 'Custom';

  const apply = useCallback(async (id: string) => {
    const target = PERMISSION_MODES.find((m) => m.id === id);
    if (!target || saving) return;
    if (target.dangerous && confirmMode?.id !== target.id) {
      setConfirmMode(target);
      return;
    }
    setSaving(true);
    setConfirmMode(null);
    setError('');
    try {
      await api.setPermissionRules(sessionId, target.rules);
      setRules(target.rules);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to set permission mode');
    } finally {
      setSaving(false);
    }
  }, [confirmMode, saving, sessionId]);

  const entries = useMemo(
    () => MENU_PERMISSION_MODES.map((m) => ({ ...m, value: m.id, iconClass: permissionModeIconClass(m.id) })),
    [],
  );

  if (rules === null) return null;

  return (
    <>
      <button
        type="button"
        className={`oc-permission-lock oc-permission-mode-${modeId || 'default'}`}
        title={error || `Permission mode: ${label}`}
        aria-label={`Permission mode: ${label}`}
        onClick={() => setPickerOpen(true)}
        disabled={saving}
        data-testid="permission-mode-lock"
      >
        <i className={`bi ${permissionModeIconClass(modeId)}`} aria-hidden="true" />
      </button>
      <CommandListPicker
        open={pickerOpen}
        entries={entries}
        fuseKeys={[{ name: 'label', weight: 1 }, { name: 'description', weight: 0.5 }]}
        renderRow={(m) => (
          <>
            <span className={`oc-permission-mode-icon oc-permission-mode-${m.id}`} aria-hidden="true"><i className={`bi ${m.iconClass}`} /></span>
            <span className="oc-permission-mode-copy">
              <span className="oc-cmd-title">{m.label}</span>
              <span className="oc-cmd-meta">{m.description}</span>
            </span>
          </>
        )}
        placeholder={() => 'Permission mode...'}
        emptyMessage="No permission modes"
        isCurrent={(m) => m.id === modeId}
        onSelect={(id) => { void apply(id); }}
        onClose={() => setPickerOpen(false)}
      />
      {error && <div className="oc-permission-lock-error">{error}</div>}
      {confirmMode && (
        <div className="oc-cmd-backdrop" onClick={() => setConfirmMode(null)}>
          <div className="oc-cmd-palette oc-permission-confirm" role="dialog" aria-label="Confirm permission mode" onClick={(e) => e.stopPropagation()}>
            <div className="oc-permission-confirm-body">
              <span className="oc-permission-mode-icon oc-permission-mode-yolo" aria-hidden="true"><i className="bi bi-exclamation-triangle" /></span>
              <div>
                <div className="oc-cmd-title">Switch to {confirmMode.label}?</div>
                <div className="oc-cmd-meta">The agent will run edits and commands without asking.</div>
              </div>
            </div>
            <div className="oc-permission-confirm-actions">
              <button type="button" className="oc-permission-confirm-cancel" onClick={() => setConfirmMode(null)}>Cancel</button>
              <button type="button" className="oc-permission-confirm-ok" onClick={() => { void apply(confirmMode.id); }}>Confirm</button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}

function permissionModeIconClass(id: string) {
  if (id === 'plan') return 'bi-book';
  if (id === 'auto-edit') return 'bi-pencil-square';
  if (id === 'yolo') return 'bi-exclamation-triangle';
  return 'bi-lock';
}
