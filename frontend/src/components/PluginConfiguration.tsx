import { useEffect, useId, useRef, useState } from 'react';
import { basePlatform } from '../lib/modelRef';
import { useProjects } from '../lib/queries';
import { useMachinePlatforms } from '../lib/useCapabilities';
import type { PluginInput, PluginRegistration } from '../lib/plugins';
import { plugins } from '../lib/plugins';
import { initialPluginValues, pluginConfigurationErrors } from '../lib/pluginConfigurationValidation';
import { SearchSelect } from './SearchSelect';
import { SettingRow } from './SettingRow';
import { Button, ButtonGroup, SelectField, TextareaField, TextField } from './Control';
import { SecretField } from './SecretField';
import styles from './PluginConfiguration.module.css';

const emptyCatalog = { agents: [] as string[], models: [] as string[] };

export function PluginConfiguration({ plugin, save, error = '', saving = false, busy = saving, focusRequest = 0, enableNeedsConfiguration = false, onDirtyChange, onCancel }: {
  plugin: PluginRegistration; save: (input: PluginInput) => Promise<void>; error?: string; saving?: boolean;
  focusRequest?: number; enableNeedsConfiguration?: boolean; busy?: boolean;
  onDirtyChange?: (dirty: boolean) => void;
  onCancel?: () => void;
}) {
  const formRef = useRef<HTMLFormElement>(null);
  const formId = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const [submitAttempt, setSubmitAttempt] = useState(0);
  useEffect(() => {
    if (busy) return;
    const invalid = formRef.current?.querySelector<HTMLElement>('[aria-invalid="true"]');
    if (invalid) invalid.focus();
    else if (error) errorRef.current?.focus();
  }, [error, busy, focusRequest, submitAttempt]);
  const settings = plugin.description.settings ?? [];
  const [values, setValues] = useState(() => initialPluginValues(plugin));
  const [secrets, setSecrets] = useState<Record<string, string>>({});
  const errors = pluginConfigurationErrors(plugin, { values, secrets });
  const invalid = Object.keys(errors).length > 0;
  const [initialValues] = useState(() => JSON.stringify(values));
  const dirty = JSON.stringify(values) !== initialValues || Object.keys(secrets).length > 0;
  useEffect(() => { onDirtyChange?.(dirty); }, [dirty, onDirtyChange]);
  const [catalog, setCatalog] = useState({ project: '', agents: [] as string[], models: [] as string[] });
  const slack = plugin.description.id === 'org.ocman.slack';
  // The catalog is tagged with the project it was fetched for, so options are
  // derived rather than cleared from an effect: a project with no answer yet
  // renders empty instead of briefly showing the previous project's lists.
  const project = slack && typeof values.project === 'string' ? values.project : '';
  // The installation's platform picks the catalog (AD-13); base ids, since the
  // owner resolves them against its own platforms.
  const platform = slack ? String(values.platform ?? '') : '';
  const platformOptions = useMachinePlatforms(plugin.ownerId).map((p) => ({ value: basePlatform(p.id), label: p.displayName }));
  const projects = (useProjects({ enabled: slack }).data ?? []).filter((item) => (item.remoteId || 'local') === plugin.ownerId && !item.archived);
  const catalogKey = `${platform}\0${project}`;
  const options = project && catalog.project === catalogKey ? catalog : emptyCatalog;

  useEffect(() => {
    if (!project) return;
    const controller = new AbortController();
    plugins.projectCatalog(plugin.ownerId, project, platform, controller.signal)
      .then((next) => setCatalog({ project: catalogKey, ...next }))
      .catch(() => setCatalog({ project: catalogKey, agents: [], models: [] }));
    return () => controller.abort();
  }, [plugin.ownerId, project, platform, catalogKey]);

  const selectValue = (key: string, value: string) => setValues((previous) => ({ ...previous, [key]: value }));
  const selectOptions = (items: string[], current: string, empty: string) => [
    { value: '', label: empty },
    ...Array.from(new Set([...items, current].filter(Boolean))).map((value) => ({ value, label: value })),
  ];

  return <form ref={formRef} noValidate className={styles.form} aria-label="Plugin configuration" aria-busy={busy} onSubmit={(event) => {
    event.preventDefault();
    if (busy) return;
    if (invalid) { setSubmitAttempt((attempt) => attempt + 1); return; }
    const input = { values, secrets };
    setSecrets({});
    void save(input);
  }}>
    {settings.map((s) => {
      const message = errors[s.key];
      const errorId = `${formId}-${s.key}-error`;
      const validation = { 'aria-invalid': message ? true : undefined, 'aria-describedby': message ? errorId : undefined };
      return <SettingRow key={s.key} label={s.label} block desc={s.secret ? 'Write-only. Leave blank to keep the current secret.' : s.required ? 'Required' : undefined}>
      <div className={styles.field}>
      {s.secret ? <SecretField {...validation} wrapperClassName={styles.control} aria-label={s.label} autoComplete="new-password" value={secrets[s.key] ?? ''}
        protect={Boolean(plugin.configuration.secrets?.[s.key])} resetPending={secrets[s.key] === ''}
        required={s.required && !plugin.configuration.secrets?.[s.key]}
        onChange={(event) => setSecrets((previous) => {
          const next = { ...previous };
          if (event.target.value) next[s.key] = event.target.value; else delete next[s.key];
          return next;
        })}
        onReset={s.required ? undefined : () => setSecrets((previous) => {
          const next = { ...previous };
          if (previous[s.key] === '') delete next[s.key]; else next[s.key] = '';
          return next;
        })} /> : slack && s.key === 'project' ? <SearchSelect {...validation} className={styles.control} ariaLabel={s.label} searchLabel="Search projects" placeholder="Select a project" value={String(values.project ?? '')}
        options={selectOptions(projects.map((project) => project.directory), String(values.project ?? ''), 'Select a project')}
        onChange={(project) => setValues((previous) => ({ ...previous, project, agent: '', model: '' }))} />
      : slack && s.key === 'platform' ? <SearchSelect {...validation} className={styles.control} ariaLabel={s.label} searchLabel="Search platforms" placeholder="Default platform" value={platform}
        options={[{ value: '', label: 'Default platform' }, ...platformOptions,
          ...(platform && !platformOptions.some((option) => option.value === platform) ? [{ value: platform, label: platform }] : [])]}
        onChange={(next) => setValues((previous) => ({ ...previous, platform: next, agent: '', model: '' }))} />
      : slack && (s.key === 'agent' || s.key === 'model') ? <SearchSelect {...validation} className={styles.control} ariaLabel={s.label} searchLabel={`Search ${s.key}s`} placeholder={`Default ${s.key}`} disabled={!values.project}
        value={String(values[s.key] ?? '')} options={selectOptions(s.key === 'agent' ? options.agents : options.models, String(values[s.key] ?? ''), `Default ${s.key}`)}
        onChange={(value) => selectValue(s.key, value)} />
      : s.type === 'boolean' ? <SelectField {...validation} className={styles.control} aria-label={s.label} required={s.required} value={String(values[s.key] ?? '')} onChange={(event) => setValues((previous) => {
        const next = { ...previous };
        if (event.target.value === '') delete next[s.key]; else next[s.key] = event.target.value === 'true';
        return next;
      })}>
        <option value="">Not set</option><option value="true">Yes</option><option value="false">No</option>
      </SelectField> : s.enum?.length ? <SelectField {...validation} className={styles.control} aria-label={s.label} required={s.required} value={String(values[s.key] ?? '')} onChange={(event) => setValues((previous) => {
        const next = { ...previous };
        if (event.target.value === '') delete next[s.key]; else next[s.key] = event.target.value;
        return next;
      })}>
        <option value="">Select a value</option>{s.enum.map((option) => <option key={option}>{option}</option>)}
      </SelectField> : s.list ? <ListRows {...validation} label={s.label} rows={Array.isArray(values[s.key]) ? values[s.key] as string[] : []}
        onChange={(rows) => setValues((previous) => ({ ...previous, [s.key]: rows }))} />
      : s.multiline ? <TextareaField {...validation} className={styles.control} aria-label={s.label} required={s.required} value={String(values[s.key] ?? '')}
        onChange={(event) => setValues((previous) => ({ ...previous, [s.key]: event.target.value }))} />
      : <TextField {...validation} className={styles.control} aria-label={s.label} type={s.type === 'string' ? 'text' : 'number'} step={s.type === 'integer' ? 1 : 'any'} required={s.required}
        value={String(values[s.key] ?? '')} onChange={(event) => setValues((previous) => {
          const next = { ...previous };
          if (s.type === 'string') next[s.key] = event.target.value;
           else if (event.target.validity.badInput) next[s.key] = NaN;
           else if (event.target.value === '') delete next[s.key];
          else next[s.key] = Number(event.target.value);
          return next;
        })} />}
      {message && <p id={errorId} className={styles.error}>{message}</p>}
      </div>
    </SettingRow>;
    })}
    {!settings.length && <p>No configuration settings.</p>}
    {error && <p role="alert" tabIndex={-1} ref={errorRef}>{error}</p>}
    {(invalid || enableNeedsConfiguration) && <p role="status">{enableNeedsConfiguration ? 'Cannot enable plugin. Fix the highlighted settings and save configuration before enabling.' : 'Fix the highlighted settings before saving configuration.'}</p>}
    <ButtonGroup label="Plugin configuration actions" className={styles.actions}>
      <Button type="submit" variant="accent" disabled={busy || invalid}>{saving ? 'Saving configuration…' : 'Save configuration'}</Button>
      {onCancel && <Button type="button" disabled={busy} onClick={onCancel}>Cancel</Button>}
    </ButtonGroup>
  </form>;
}

// List settings post a JSON array of strings; each row is one item.
function ListRows({ label, rows, onChange, ...validation }: { label: string; rows: string[]; onChange: (rows: string[]) => void; 'aria-invalid'?: boolean; 'aria-describedby'?: string }) {
  return <div {...validation} role="group" aria-label={label} tabIndex={-1} className={`${styles.control} ${styles.list}`}>
    {rows.map((row, index) => <div key={index}>
      <TextField aria-label={`${label} ${index + 1}`} value={row} onChange={(event) => onChange(rows.map((item, i) => i === index ? event.target.value : item))} />
      <Button type="button" size="small" variant="ghost" onClick={() => onChange(rows.filter((_, i) => i !== index))}>Remove {label} {index + 1}</Button>
    </div>)}
    <Button type="button" size="small" variant="ghost" onClick={() => onChange([...rows, ''])}>Add {label}</Button>
  </div>;
}
