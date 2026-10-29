import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { api, type PermissionRule } from '../lib/api';
import { classifyPermissionMode, PERMISSION_MODES, type PermissionMode } from '../lib/permissionModes';
import { Button, SubmitButton } from './Control';
import { CommandListPicker } from './assistant/CommandListPicker';
import { Modal } from './Modal';
import { onSessionChanged } from '../lib/useGlobalEvents';
import './PermissionModeLock.css';

/**
 * Session permission-mode lock: shows the current permission posture
 * and lets the user switch between presets. Renders only when the caller has already gated
 * on caps.permissionRules and a live connection.
 */
export function PermissionModeLock({ sessionId }: { sessionId: string }) {
  const [rules, setRules] = useState<PermissionRule[] | null>(null);
  const [nativeMode, setNativeMode] = useState<string | null>(null);
  const [nativePending, setNativePending] = useState(false);
  const activeSession = useRef(sessionId);
  useEffect(() => { activeSession.current = sessionId; }, [sessionId]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [pickerOpen, setPickerOpen] = useState(false);
  const [confirmMode, setConfirmMode] = useState<PermissionMode | null>(null);

  const [prevSessionId, setPrevSessionId] = useState(sessionId);
  if (prevSessionId !== sessionId) {
    setPrevSessionId(sessionId);
    setRules(null);
    setNativeMode(null);
    setNativePending(false);
    setError('');
  }

  useEffect(() => {
    let cancelled = false;
    let revision = 0;
    const load = () => {
      const current = ++revision;
      void api.getPermissionRules(sessionId)
        .then((resp) => {
          if (!cancelled && current === revision) {
            setRules(resp.rules);
            setNativeMode(resp.nativeMode?.mode ?? null);
            setNativePending(resp.nativeMode?.pending ?? false);
          }
        })
        .catch(() => {
          // No live instance / transient error: hide the passive control.
          if (!cancelled && current === revision) setRules(null);
        });
    };
    load();
    const unsubscribe = onSessionChanged((id) => { if (id === sessionId) load(); });
    window.addEventListener('focus', load);
    return () => {
      cancelled = true;
      unsubscribe();
      window.removeEventListener('focus', load);
    };
  }, [sessionId]);

  useEffect(() => {
    const open = () => setPickerOpen(true);
    window.addEventListener('oc-permission-mode-open', open);
    return () => window.removeEventListener('oc-permission-mode-open', open);
  }, []);

  const modeId = rules === null ? '' : classifyPermissionMode(rules);
  const mode = PERMISSION_MODES.find((m) => m.id === modeId);
  const rulesLabel = mode ? mode.label : 'Custom';
  const label = nativeMode === 'auto' ? `${rulesLabel} + native Auto${nativePending ? ' (next turn)' : ''}` : rulesLabel;

  const apply = useCallback(async (id: string) => {
    if (id === 'native-auto' || id === 'native-default') {
      if (saving) return;
      setSaving(true);
      setError('');
      try {
        const next = id === 'native-auto' ? 'auto' : 'default';
        await api.setNativePermissionMode(sessionId, next);
        const response = await api.getPermissionRules(sessionId);
        if (activeSession.current === sessionId) {
          setNativeMode(response.nativeMode?.mode ?? null);
          setNativePending(response.nativeMode?.pending ?? false);
        }
      } catch (e) {
        if (activeSession.current === sessionId) setError(e instanceof Error ? e.message : 'failed to set native permission mode');
      } finally { setSaving(false); }
      return;
    }
    const target = PERMISSION_MODES.find((m) => m.id === id);
    if (!target || saving) return;
    if (target.dangerous && confirmMode?.id !== target.id) {
      setConfirmMode(target);
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
      setConfirmMode(null);
    }
  }, [confirmMode, saving, sessionId]);

  const entries = useMemo(
    () => {
      const modes = [...PERMISSION_MODES];
      if (nativeMode !== null) modes.push({
        id: nativeMode === 'auto' ? 'native-default' : 'native-auto',
        label: 'Native Auto',
        description: `${nativePending ? 'Applies on the next turn. ' : ''}Claude reviews tool calls. Ocman rules and auto-approval stay separate. Applies on next turn when idle; requires a supported model.`,
        rules: [],
      });
      return modes.map((m) => ({ ...m, value: m.id, iconClass: permissionModeIconClass(m.id) }));
    },
    [nativeMode, nativePending],
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
        sectionOf={nativeMode !== null ? (m) => m.id.startsWith('native-') ? 'Platform modes' : 'Ocman modes' : undefined}
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
        isCurrent={(m) => m.id === modeId || (m.id === 'native-default' && nativeMode === 'auto')}
        onSelect={(id) => { void apply(id); }}
        onClose={() => setPickerOpen(false)}
      />
      {error && <div className="oc-permission-lock-error">{error}</div>}
      {confirmMode && <PermissionModeConfirmation mode={confirmMode} saving={saving} onClose={() => setConfirmMode(null)} onConfirm={() => apply(confirmMode.id)} />}
    </>
  );
}

function PermissionModeConfirmation({ mode, saving, onClose, onConfirm }: {
  mode: PermissionMode; saving: boolean; onClose: () => void; onConfirm: () => Promise<void>;
}) {
  // Match the picker: disabled composer styles must not dim the modal or backdrop.
  return createPortal(
    <Modal
      label="Confirm permission mode"
      backdropClassName="oc-cmd-backdrop oc-permission-confirm-backdrop"
      dialogClassName="oc-cmd-palette oc-permission-confirm"
      onClose={onClose}
      canClose={!saving}
    >
      <div className="oc-permission-confirm-body">
        <span className="oc-permission-mode-icon oc-permission-mode-yolo" aria-hidden="true"><i className="bi bi-exclamation-triangle" /></span>
        <div>
          <div className="oc-cmd-title">Switch to {mode.label}?</div>
          <div className="oc-cmd-meta">The agent will run edits and commands without asking.</div>
        </div>
      </div>
      <div className="oc-permission-confirm-actions">
        <Button size="small" disabled={saving} onClick={onClose}>Cancel</Button>
        <SubmitButton size="small" variant="danger" data-autofocus pendingLabel="Applying…" onClick={onConfirm}>Confirm</SubmitButton>
      </div>
    </Modal>,
    document.body,
  );
}

function permissionModeIconClass(id: string) {
  if (id === 'plan') return 'bi-book';
  if (id === 'auto-edit') return 'bi-pencil-square';
  if (id === 'yolo') return 'bi-exclamation-triangle';
  return 'bi-lock';
}
