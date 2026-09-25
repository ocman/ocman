import type { PermissionRule } from '../lib/api.types';
import { PERMISSION_MODES, classifyPermissionMode } from '../lib/permissionModes';
import { ButtonGroup } from './Control';
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
}

export function PermissionRulesEditor({ rules, onChange, disabled }: Props) {
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
    <div className="perm-rules-editor" aria-label="Permission rules">
      {/* Preset strip */}
      <ButtonGroup label="Permission presets">
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
      </ButtonGroup>

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
                  <input
                    aria-label={`Rule ${i + 1} permission`}
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
                  <input
                    aria-label={`Rule ${i + 1} pattern`}
                    value={rule.pattern}
                    disabled={disabled}
                    placeholder="*"
                    onChange={(e) => setRule(i, { pattern: e.target.value })}
                  />
                </div>

                {/* Action */}
                <div className="perm-rules-cell">
                  <select
                    aria-label={`Rule ${i + 1} action`}
                    value={rule.action}
                    disabled={disabled}
                    onChange={(e) => setRule(i, { action: e.target.value as PermissionRule['action'] })}
                  >
                    {ACTIONS.map((a) => (
                      <option key={a} value={a}>{a}</option>
                    ))}
                  </select>
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
          No rules — the session uses platform defaults.
        </p>
      )}
    </div>
  );
}
