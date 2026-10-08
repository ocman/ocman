import { type ComponentProps, type ReactNode } from 'react';
import { SaveStatus } from './SaveStatus';
import styles from './SettingRow.module.css';
import { TextField } from './Control';
import { ToggleField } from './ToggleField';
import { SearchSelect, type SearchSelectOption } from './SearchSelect';
import { useSettingSave } from '../lib/useSaveStatus';
import { settingAnchor, settingEntry, type SettingId } from '../lib/settingsCatalog';

/**
 * SettingRow + its typed controls are the ONLY sanctioned way to render a
 * settings field. The save-status indicator (spinner while in-flight,
 * checkmark after) lives on the row and is driven by the control's own save
 * call — so a new setting cannot be added without its GitHub-style feedback.
 * Never hand-roll `settings-row` markup; add controls here instead.
 */

type Save = ReturnType<typeof useSettingSave>;

export function SettingDescription({ className = '', ...props }: ComponentProps<'p'>) {
  return <p {...props} className={`${styles.description} ${className}`} />;
}

/**
 * A configurable setting passes `setting`: its title, description and example
 * come from the settings catalog (so it is searchable), and `label`/`desc`
 * only override them for live state. Rows without an id are informational
 * or generated from data (plugin fields, provider accounts) and pass `label`.
 */
type RowCopy =
  | { setting: SettingId; label?: ReactNode; desc?: ReactNode }
  | { setting?: undefined; label: ReactNode; desc?: ReactNode };

export function SettingRow({
  setting,
  label,
  desc,
  detail,
  block,
  children,
}: RowCopy & {
  /** Live status shown under the description, e.g. a path and size. */
  detail?: ReactNode;
  block?: boolean;
  children: ReactNode;
}) {
  const entry = setting ? settingEntry(setting) : undefined;
  const description = desc ?? entry?.description;
  return (
    <div
      id={setting ? settingAnchor(setting) : undefined}
      className={block ? `settings-row settings-row--block ${styles.row} ${styles.block}` : `settings-row ${styles.row}`}
    >
      <div className={styles.info}>
        <div className={styles.label}>{label ?? entry?.title}</div>
        {description != null && <div className={styles.description}>{description}</div>}
        {detail != null && <div className={styles.description}>{detail}</div>}
        {entry && (
          <details className={styles.example}>
            <summary>Example</summary>
            <p>{entry.example}</p>
          </details>
        )}
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
  ariaLabel: string;
  testId?: string;
}) {
  return (
    <>
      <ToggleField
        label={ariaLabel}
        data-testid={testId}
        checked={checked}
        disabled={disabled}
        onChange={(e) => {
          const next = e.target.checked;
          void save.track(() => withMinSpinner(() => onSave(next))).catch(() => {});
        }}
      />
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
    <div className={styles.numberInput}>
      <TextField className={styles.number}
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
      <span className={styles.unit}>{unit}</span>
      <SaveStatus state={save.state} />
    </div>
  );
}

/**
 * SettingSelect is a searchable picker that runs `onSave` and shows save
 * status. The current value is always an option, so a value the catalog
 * no longer offers is still displayed rather than silently blanked.
 */
export type SettingSelectProps = {
  value: string;
  options: SearchSelectOption[];
  save: Save;
  onSave: (next: string) => void | Promise<unknown>;
  ariaLabel: string;
  placeholder: string;
  searchLabel: string;
  disabled?: boolean;
};

export function SettingSelect({
  value,
  options,
  save,
  onSave,
  ariaLabel,
  placeholder,
  searchLabel,
  disabled,
}: SettingSelectProps) {
  const known = !value || options.some((option) => option.value === value);
  const all = known ? options : [...options, { value, label: value }];
  return (
    <div className={styles.selectInput}>
      <SearchSelect
        className={styles.picker}
        value={value}
        options={all}
        ariaLabel={ariaLabel}
        placeholder={placeholder}
        searchLabel={searchLabel}
        disabled={disabled}
        onChange={(next) => {
          if (next === value) return;
          void save.track(() => withMinSpinner(() => onSave(next))).catch(() => {});
        }}
      />
      <SaveStatus state={save.state} />
    </div>
  );
}

/**
 * SettingText is a text input that saves on blur or Enter when the value
 * changed. Secrets use `type="password"`; pass `value=""` with a placeholder
 * describing the stored state so the saved value never reaches the page.
 */
export function SettingText({
  value,
  save,
  onSave,
  ariaLabel,
  placeholder,
  type = 'text',
  disabled,
}: {
  value: string;
  save: Save;
  onSave: (next: string) => void | Promise<unknown>;
  ariaLabel: string;
  placeholder?: string;
  type?: 'text' | 'password' | 'url';
  disabled?: boolean;
}) {
  const commit = (next: string) => {
    if (next === value) return;
    void save.track(() => withMinSpinner(() => onSave(next))).catch(() => {});
  };
  return (
    <div className={styles.textInput}>
      <TextField className={styles.text}
        key={value}
        type={type}
        aria-label={ariaLabel}
        placeholder={placeholder}
        defaultValue={value}
        disabled={disabled}
        autoComplete="off"
        onBlur={(e) => commit(e.target.value)}
        onKeyDown={(e) => { if (e.key === 'Enter') commit(e.currentTarget.value); }}
      />
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
