import { useCallback, useEffect, useState } from 'react';
import './LoopEditModal.css';
import type { Loop, LoopUpdateRequest } from '../lib/api.types';
import { formatGoDuration, parseGoDuration } from '../lib/loopFormat';

interface LoopEditModalProps {
  loop: Loop;
  onSave: (req: LoopUpdateRequest) => Promise<void>;
  onClose: () => void;
  // Lifecycle actions, surfaced in the modal footer. Optional so the
  // modal still works in contexts that only edit settings.
  onPause?: () => Promise<void>;
  onResume?: () => Promise<void>;
  onTrigger?: () => Promise<void>;
  onDelete?: () => Promise<void>;
}

/**
 * LoopEditModal edits a loop's safe-to-change settings (title, action
 * prompt, session mode, interval, budget) and hosts the loop's lifecycle
 * controls (pause/resume, trigger now, delete). Trigger type and action
 * type are fixed post-create. Shared by the sidebar LoopsPane and the
 * /loops cards so there is a single edit UX.
 */
export function LoopEditModal({ loop, onSave, onClose, onPause, onResume, onTrigger, onDelete }: LoopEditModalProps) {
  const isSchedule = loop.triggerType === 'schedule';
  const [confirmDelete, setConfirmDelete] = useState(false);
  const canTrigger = isSchedule && loop.state === 'active';
  const [title, setTitle] = useState(loop.title);
  const [template, setTemplate] = useState(loop.actionTemplate ?? '');
  const [sessionMode, setSessionMode] = useState(loop.sessionMode || 'fresh');
  const [interval, setInterval] = useState(
    formatGoDuration(loop.triggerConfig?.interval_seconds ?? 60),
  );
  const [maxIters, setMaxIters] = useState(String(loop.stopConditions?.max_iterations ?? 25));
  const [maxCost, setMaxCost] = useState(
    loop.stopConditions?.max_cost_usd != null ? String(loop.stopConditions.max_cost_usd) : '',
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Escape closes the modal (matches MachinePickerModal).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !saving) onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose, saving]);

  const submit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      setError(null);
      const maxCostNum = maxCost.trim() === '' ? undefined : Number(maxCost);
      const maxTokensExisting = loop.stopConditions?.max_tokens;
      // Budget invariant: must keep a cost cap or an existing token cap.
      if ((maxCostNum == null || maxCostNum <= 0) && !maxTokensExisting) {
        setError('A budget is required: set a max cost.');
        return;
      }

      let intervalSeconds = 0;
      if (isSchedule) {
        const parsed = parseGoDuration(interval);
        if (parsed == null) {
          setError('Interval must be a duration like 30s, 5m, or 1h30m.');
          return;
        }
        intervalSeconds = parsed;
      }

      const req: LoopUpdateRequest = {
        title,
        action_template: template,
        session_mode: sessionMode,
        stop_conditions: {
          max_iterations: Number(maxIters) || 25,
          max_cost_usd: maxCostNum,
          max_tokens: maxTokensExisting,
          max_duration: loop.stopConditions?.max_duration,
          error_streak: loop.stopConditions?.error_streak,
        },
      };
      if (isSchedule) {
        req.trigger_config = { interval_seconds: intervalSeconds };
      }
      setSaving(true);
      try {
        await onSave(req);
        onClose();
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setSaving(false);
      }
    },
    [title, template, sessionMode, interval, maxIters, maxCost, isSchedule, loop, onSave, onClose],
  );

  return (
    <div
      className="oc-loop-modal-backdrop"
      data-testid="loop-edit-backdrop"
      onClick={() => !saving && onClose()}
    >
      <div
        className="oc-loop-modal"
        role="dialog"
        aria-label="Edit loop settings"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="oc-loop-modal-title">Edit loop</div>
        <form className="oc-loop-modal-form" data-testid="loop-edit-form" onSubmit={submit}>
          <label>
            Title
            <input value={title} onChange={(e) => setTitle(e.target.value)} />
          </label>
          <label>
            Action prompt
            <textarea
              value={template}
              rows={4}
              onChange={(e) => setTemplate(e.target.value)}
              placeholder="Prompt sent each iteration. Placeholders: {{iteration}} {{last_summary}}"
            />
          </label>
          <label>
            Session per iteration
            <select value={sessionMode} onChange={(e) => setSessionMode(e.target.value)}>
              <option value="fresh">Fresh (new session each time)</option>
              <option value="reuse">Reuse (one ongoing session)</option>
            </select>
          </label>
          {isSchedule && (
            <>
              <label>
                Interval
                <input
                  type="text"
                  inputMode="text"
                  placeholder="e.g. 30s, 5m, 1h30m"
                  value={interval}
                  onChange={(e) => setInterval(e.target.value)}
                />
              </label>
              <span className="oc-loop-modal-hint">
                Go-style duration. Minimum 60s; shorter values are raised to 60s.
              </span>
            </>
          )}
          <label>
            Max iterations
            <input type="number" min={1} value={maxIters} onChange={(e) => setMaxIters(e.target.value)} />
          </label>
          <label>
            Max cost (USD)
            <input
              type="number"
              min={0}
              step="0.5"
              value={maxCost}
              onChange={(e) => setMaxCost(e.target.value)}
            />
          </label>
          {error && <div className="oc-loop-modal-error">{error}</div>}
          <div className="oc-loop-modal-actions">
            <button type="submit" className="vscode-btn oc-loop-primary" disabled={saving}>
              {saving ? 'Saving…' : 'Save'}
            </button>
            <button type="button" className="vscode-btn" onClick={onClose} disabled={saving}>
              Cancel
            </button>
          </div>
        </form>

        <div className="oc-loop-modal-lifecycle" data-testid="loop-lifecycle">
          {canTrigger && onTrigger && (
            <button type="button" className="vscode-btn" onClick={() => void onTrigger()}>
              Trigger now
            </button>
          )}
          {loop.state === 'active' && onPause && (
            <button type="button" className="vscode-btn" onClick={() => void onPause()}>
              Pause
            </button>
          )}
          {loop.state === 'paused' && onResume && (
            <button type="button" className="vscode-btn" onClick={() => void onResume()}>
              Resume
            </button>
          )}
          {onDelete && (
            confirmDelete ? (
              <span className="oc-loop-confirm-delete">
                Delete this loop?
                <button
                  type="button"
                  className="vscode-btn oc-loop-danger"
                  onClick={async () => {
                    await onDelete();
                    onClose();
                  }}
                >
                  Confirm
                </button>
                <button type="button" className="vscode-btn" onClick={() => setConfirmDelete(false)}>
                  Keep
                </button>
              </span>
            ) : (
              <button
                type="button"
                className="vscode-btn oc-loop-danger"
                onClick={() => setConfirmDelete(true)}
              >
                Delete
              </button>
            )
          )}
        </div>
      </div>
    </div>
  );
}
