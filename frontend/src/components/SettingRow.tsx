import { type ReactNode } from 'react';
import { SaveStatus } from './SaveStatus';
import { useSettingSave } from '../lib/useSaveStatus';

/**
 * SettingRow + its typed controls are the ONLY sanctioned way to render a
 * settings field. The save-status indicator (spinner while in-flight,
 * checkmark after) lives on the row and is driven by the control's own save
 * call — so a new setting cannot be added without its GitHub-style feedback.
 * Never hand-roll `settings-row` markup; add controls here instead.
 */

type Save = ReturnType<typeof useSettingSave>;

export function SettingRow({
  label,
  desc,
  block,
  children,
}: {
  label: ReactNode;
  desc?: ReactNode;
  block?: boolean;
  children: ReactNode;
}) {
  return (
    <div className={block ? 'settings-row settings-row--block' : 'settings-row'}>
      <div className="settings-row-info">
        <div className="settings-row-label">{label}</div>
        {desc != null && <div className="settings-row-desc">{desc}</div>}
      </div>
      {children}
    </div>
  );
}

/**
 * SettingToggle is a checkbox that runs `onSave(next)` and shows save status.
 * `onSave` may be sync (localStorage) or async (API); either way the row
 * flashes a spinner then a checkmark. A minimum spinner time keeps the
 * feedback visible for instant sync saves.
 */
export function SettingToggle({
  checked,
  disabled,
  save,
  onSave,
  ariaLabel,
  testId,
}: {
  checked: boolean;
  disabled?: boolean;
  save: Save;
  onSave: (next: boolean) => void | Promise<unknown>;
  ariaLabel?: string;
  testId?: string;
}) {
  return (
    <>
      <label className="settings-toggle">
        <input
          type="checkbox"
          aria-label={ariaLabel}
          data-testid={testId}
          checked={checked}
          disabled={disabled}
          onChange={(e) => {
            const next = e.target.checked;
            void save.track(() => withMinSpinner(() => onSave(next))).catch(() => {});
          }}
        />
        <span className="settings-toggle-track" />
      </label>
      <SaveStatus state={save.state} />
    </>
  );
}

/**
 * SettingNumber is a numeric input with a unit suffix that runs `onSave` and
 * shows save status. `parse` maps the raw input value to the saved value.
 */
export function SettingNumber({
  value,
  unit,
  min,
  max,
  step = 1,
  save,
  parse,
  onSave,
  ariaLabel,
  disabled,
}: {
  value: number;
  unit: string;
  min?: number;
  max?: number;
  step?: number;
  save: Save;
  parse?: (raw: number) => number;
  onSave: (next: number) => void | Promise<unknown>;
  ariaLabel?: string;
  disabled?: boolean;
}) {
  return (
    <div className="settings-delay-input">
      <input
        type="number"
        min={min}
        max={max}
        step={step}
        aria-label={ariaLabel}
        disabled={disabled}
        value={value}
        onChange={(e) => {
          const raw = Number(e.target.value) || 0;
          const next = parse ? parse(raw) : raw;
          void save.track(() => withMinSpinner(() => onSave(next))).catch(() => {});
        }}
      />
      <span className="settings-delay-unit">{unit}</span>
      <SaveStatus state={save.state} />
    </div>
  );
}

/** Hold the spinner briefly so instant (sync) saves still flash it. */
async function withMinSpinner<T>(run: () => T | Promise<T>): Promise<T> {
  const [result] = await Promise.all([
    Promise.resolve(run()),
    new Promise((r) => setTimeout(r, 300)),
  ]);
  return result;
}
