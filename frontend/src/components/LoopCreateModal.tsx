import { useCallback, useEffect, useState } from 'react';
import './LoopEditModal.css';
import type { LoopCreateRequest } from '../lib/api.types';
import { parseGoDuration } from '../lib/loopFormat';

interface LoopCreateModalProps {
  // Session the loop is anchored to (its creator/owner session).
  rootSessionId: string;
  platform?: string;
  directory?: string;
  // Optional parent loop id to create a sub-loop.
  parentLoopId?: string;
  onCreate: (req: LoopCreateRequest) => Promise<void>;
  onClose: () => void;
}

type Trigger = 'schedule' | 'cron' | 'pr_event' | 'turn_complete' | 'child_complete';
type Action = 'prompt_root' | 'prompt_child' | 'spawn_child' | 'spawn_worktree';

/**
 * LoopCreateModal authors a new agent loop. Exposes every trigger and
 * action type the backend supports (previously only reachable via MCP).
 * A budget is mandatory (backend rejects loops without one).
 */
export function LoopCreateModal({
  rootSessionId,
  platform,
  directory,
  parentLoopId,
  onCreate,
  onClose,
}: LoopCreateModalProps) {
  const [title, setTitle] = useState('');
  const [trigger, setTrigger] = useState<Trigger>('schedule');
  const [interval, setInterval] = useState('30m');
  const [cronExpr, setCronExpr] = useState('0 23 * * *');
  const [prNumber, setPrNumber] = useState('');
  const [action, setAction] = useState<Action>('prompt_root');
  const [targetSession, setTargetSession] = useState('');
  const [sessionMode, setSessionMode] = useState('fresh');
  const [model, setModel] = useState('');
  const [template, setTemplate] = useState('');
  const [maxIters, setMaxIters] = useState('25');
  const [maxCost, setMaxCost] = useState('5');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

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

      const cost = maxCost.trim() === '' ? undefined : Number(maxCost);
      if (cost == null || cost <= 0) {
        setError('A budget is required: set a max cost.');
        return;
      }

      const triggerConfig: LoopCreateRequest['trigger_config'] = {};
      if (trigger === 'schedule') {
        const secs = parseGoDuration(interval);
        if (secs == null) {
          setError('Interval must be a duration like 30s, 5m, or 1h30m.');
          return;
        }
        triggerConfig.interval_seconds = secs;
      }
      if (trigger === 'cron') {
        if (cronExpr.trim().split(/\s+/).length !== 5) {
          setError('Cron must have 5 fields, e.g. "0 23 * * *" (daily at 23:00).');
          return;
        }
        triggerConfig.cron_expr = cronExpr.trim();
      }
      if (trigger === 'pr_event') {
        const n = Number(prNumber);
        if (!n || n <= 0) {
          setError('PR number is required for a pr_event trigger.');
          return;
        }
        triggerConfig.pr_number = n;
      }
      if (action === 'prompt_child') {
        if (targetSession.trim() === '') {
          setError('Target session id is required for prompt_child.');
          return;
        }
        triggerConfig.target_session_id = targetSession.trim();
      }

      const req: LoopCreateRequest = {
        root_session_id: rootSessionId,
        parent_loop_id: parentLoopId,
        platform,
        directory,
        title: title.trim() || undefined,
        trigger_type: trigger,
        trigger_config: triggerConfig,
        action_type: action,
        action_template: template.trim() || undefined,
        model: model.trim() || undefined,
        session_mode: action === 'prompt_root' ? sessionMode : undefined,
        stop_conditions: {
          max_iterations: Number(maxIters) || 25,
          max_cost_usd: cost,
        },
      };

      setSaving(true);
      try {
        await onCreate(req);
        onClose();
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setSaving(false);
      }
    },
    [
      title, trigger, interval, cronExpr, prNumber, action, targetSession, sessionMode, model,
      template, maxIters, maxCost, rootSessionId, parentLoopId, platform, directory,
      onCreate, onClose,
    ],
  );

  return (
    <div
      className="oc-loop-modal-backdrop"
      data-testid="loop-create-backdrop"
      onClick={() => !saving && onClose()}
    >
      <div
        className="oc-loop-modal"
        role="dialog"
        aria-label="Create loop"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="oc-loop-modal-title">{parentLoopId ? 'New sub-loop' : 'New loop'}</div>
        <form className="oc-loop-modal-form" data-testid="loop-create-form" onSubmit={submit}>
          <label>
            Title
            <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="e.g. Watch PR #42" />
          </label>

          <label>
            Trigger
            <select value={trigger} onChange={(e) => setTrigger(e.target.value as Trigger)}>
              <option value="schedule">Schedule (interval)</option>
              <option value="cron">Cron (time of day)</option>
              <option value="pr_event">PR event (head change / merge)</option>
              <option value="turn_complete">Turn complete (session goes idle)</option>
              <option value="child_complete">Child complete</option>
            </select>
          </label>
          {trigger === 'schedule' && (
            <>
              <label>
                Interval
                <input
                  type="text"
                  value={interval}
                  onChange={(e) => setInterval(e.target.value)}
                  placeholder="e.g. 30s, 5m, 1h30m"
                />
              </label>
              <span className="oc-loop-modal-hint">Go-style duration. Minimum 60s.</span>
            </>
          )}
          {trigger === 'cron' && (
            <>
              <label>
                Cron expression
                <input
                  type="text"
                  value={cronExpr}
                  onChange={(e) => setCronExpr(e.target.value)}
                  placeholder="0 23 * * *"
                />
              </label>
              <span className="oc-loop-modal-hint">
                5-field cron (min hour dom month dow), server-local time. "0 23 * * *" = daily at 23:00.
              </span>
            </>
          )}
          {trigger === 'pr_event' && (
            <label>
              PR number
              <input type="number" min={1} value={prNumber} onChange={(e) => setPrNumber(e.target.value)} />
            </label>
          )}

          <label>
            Action
            <select value={action} onChange={(e) => setAction(e.target.value as Action)}>
              <option value="prompt_root">Prompt a dedicated session</option>
              <option value="prompt_child">Prompt a specific session</option>
              <option value="spawn_child">Spawn a new session</option>
              <option value="spawn_worktree">Spawn a worktree session</option>
            </select>
          </label>
          {action === 'prompt_child' && (
            <label>
              Target session id
              <input value={targetSession} onChange={(e) => setTargetSession(e.target.value)} />
            </label>
          )}
          {action === 'prompt_root' && (
            <label>
              Session per iteration
              <select value={sessionMode} onChange={(e) => setSessionMode(e.target.value)}>
                <option value="fresh">Fresh (new session each time)</option>
                <option value="reuse">Reuse (one ongoing session)</option>
              </select>
            </label>
          )}

          <label>
            Model (optional)
            <input value={model} onChange={(e) => setModel(e.target.value)} placeholder="provider/model" />
          </label>

          <label>
            Action prompt
            <textarea
              value={template}
              rows={4}
              onChange={(e) => setTemplate(e.target.value)}
              placeholder="Prompt sent each iteration. Placeholders: {{iteration}} {{last_summary}} {{pr_number}}"
            />
          </label>

          <label>
            Max iterations
            <input type="number" min={1} value={maxIters} onChange={(e) => setMaxIters(e.target.value)} />
          </label>
          <label>
            Max cost (USD)
            <input type="number" min={0} step="0.5" value={maxCost} onChange={(e) => setMaxCost(e.target.value)} />
          </label>

          {error && <div className="oc-loop-modal-error">{error}</div>}
          <div className="oc-loop-modal-actions">
            <button type="submit" className="vscode-btn oc-loop-primary" disabled={saving}>
              {saving ? 'Creating…' : 'Create'}
            </button>
            <button type="button" className="vscode-btn" onClick={onClose} disabled={saving}>
              Cancel
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
