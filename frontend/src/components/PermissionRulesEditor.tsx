import type { PermissionRule } from '../lib/api.types';
import { PERMISSION_MODES, classifyPermissionMode } from '../lib/permissionModes';
import { ButtonGroup, SelectField, TextField } from './Control';
import { IconButton } from './IconButton';
import './PermissionRulesEditor.css';

const KNOWN_PERMISSIONS = ['*', 'bash', 'edit', 'external_directory'];
const ACTIONS = ['allow', 'deny', 'ask'] as const;

function ruleError(rule: PermissionRule): string | null {
  if (!rule.permission.trim()) return 'Permission is required';
  if (!rule.pattern.trim()) return 'Pattern is required';
  if (!ACTIONS.includes(rule.action as (typeof ACTIONS)[number])) return 'Invalid action';
  return null;
}

interface Props {
  rules: PermissionRule[];
  onChange: (rules: PermissionRule[]) => void;
  disabled?: boolean;
  presets?: boolean;
  reorderable?: boolean;
  emptyMessage?: string;
}

export function PermissionRulesEditor({ rules, onChange, disabled, presets = true, reorderable = false, emptyMessage = 'No rules — the session uses platform defaults.' }: Props) {
  const modeId = classifyPermissionMode(rules);

  const setRule = (i: number, patch: Partial<PermissionRule>) => {
    const next = rules.map((r, j) => (j === i ? { ...r, ...patch } : r));
    onChange(next);
  };

  const removeRule = (i: number) => onChange(rules.filter((_, j) => j !== i));

  const addRule = () =>
    onChange([...rules, { permission: 'bash', pattern: '*', action: 'allow' }]);

  const applyPreset = (id: string) => {
    const mode = PERMISSION_MODES.find((m) => m.id === id);
    if (mode) onChange([...mode.rules]);
  };

  return (
    <div className={`perm-rules-editor${reorderable ? ' perm-rules-editor--reorderable' : ''}`} aria-label="Permission rules">
      {/* Preset strip */}
      {presets && <ButtonGroup label="Permission presets">
        {PERMISSION_MODES.map((mode) => (
          <button
            key={mode.id}
            type="button"
            disabled={disabled}
            className={`perm-rules-preset${modeId === mode.id ? ' perm-rules-preset--active' : ''}${mode.dangerous ? ' perm-rules-preset--danger' : ''}`}
            title={mode.description}
            onClick={() => applyPreset(mode.id)}
          >
            {mode.label}
          </button>
        ))}
        {modeId === 'custom' && (
          <span className="perm-rules-preset perm-rules-preset--active perm-rules-preset--custom" aria-current="true">
            Custom
          </span>
        )}
      </ButtonGroup>}

      {/* Rule rows */}
      {rules.length > 0 && (
        <div className="perm-rules-list" role="list">
          {/* Column headers */}
          <div className="perm-rules-header" aria-hidden="true">
            <span>Permission</span>
            <span>Pattern</span>
            <span>Action</span>
          </div>

          {rules.map((rule, i) => {
            const err = ruleError(rule);
            return (
              <div key={i} className={`perm-rules-row${err ? ' perm-rules-row--invalid' : ''}`} role="listitem">
                {/* Permission */}
                <div className="perm-rules-cell">
                  <TextField
                    aria-label={`Rule ${i + 1} permission`}
                    aria-invalid={!rule.permission.trim()}
                    list={`perm-permissions-${i}`}
                    value={rule.permission}
                    disabled={disabled}
                    placeholder="bash"
                    onChange={(e) => setRule(i, { permission: e.target.value })}
                  />
                  <datalist id={`perm-permissions-${i}`}>
                    {KNOWN_PERMISSIONS.map((p) => <option key={p} value={p} />)}
                  </datalist>
                </div>

                {/* Pattern */}
                <div className="perm-rules-cell">
                  <TextField
                    aria-label={`Rule ${i + 1} pattern`}
                    aria-invalid={!rule.pattern.trim()}
                    value={rule.pattern}
                    disabled={disabled}
                    placeholder="*"
                    onChange={(e) => setRule(i, { pattern: e.target.value })}
                  />
                </div>

                {/* Action */}
                <div className="perm-rules-cell">
                  <SelectField
                    aria-label={`Rule ${i + 1} action`}
                    aria-invalid={!ACTIONS.includes(rule.action as (typeof ACTIONS)[number])}
                    value={rule.action}
                    disabled={disabled}
                    onChange={(e) => setRule(i, { action: e.target.value as PermissionRule['action'] })}
                  >
                    {ACTIONS.map((a) => (
                      <option key={a} value={a}>{a}</option>
                    ))}
                  </SelectField>
                </div>

                {/* Remove */}
                <button
                  type="button"
                  disabled={disabled}
                  className="perm-rules-remove"
                  aria-label={`Remove rule ${i + 1}`}
                  title="Remove rule"
                  onClick={() => removeRule(i)}
                >
                  <i className="bi bi-x" aria-hidden="true" />
                </button>

                {err && <p className="perm-rules-row-error" role="alert">{err}</p>}
                {reorderable && <ButtonGroup className="perm-rules-order" label={`Move rule ${i + 1}`}>
                  <IconButton icon="bi-arrow-up" variant="ghost" disabled={disabled || i === 0} label={`Move rule ${i + 1} up`} onClick={() => {
                    const next = [...rules];
                    [next[i - 1], next[i]] = [next[i], next[i - 1]];
                    onChange(next);
                  }} />
                  <IconButton icon="bi-arrow-down" variant="ghost" disabled={disabled || i === rules.length - 1} label={`Move rule ${i + 1} down`} onClick={() => {
                    const next = [...rules];
                    [next[i], next[i + 1]] = [next[i + 1], next[i]];
                    onChange(next);
                  }} />
                </ButtonGroup>}
              </div>
            );
          })}
        </div>
      )}

      {/* Add row */}
      <button
        type="button"
        disabled={disabled}
        className="perm-rules-add"
        onClick={addRule}
      >
        <i className="bi bi-plus" aria-hidden="true" /> Add rule
      </button>

      {rules.length === 0 && (
        <p className="perm-rules-empty">
          {emptyMessage}
        </p>
      )}
    </div>
  );
}
