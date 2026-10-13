import { useEffect, useState } from 'react';
import { api, type Project } from '../lib/api';
import { useMachinePlatforms } from '../lib/useCapabilities';
import type { PluginInput, PluginRegistration } from '../lib/plugins';
import { plugins } from '../lib/plugins';
import { SearchSelect } from './SearchSelect';
import { SettingRow } from './SettingRow';
import { Button, SelectField, TextField } from './Control';
import { SecretField } from './SecretField';
import styles from './PluginConfiguration.module.css';

const emptyCatalog = { agents: [] as string[], models: [] as string[] };

export function PluginConfiguration({ plugin, save }: { plugin: PluginRegistration; save: (input: PluginInput) => Promise<void> }) {
  const settings = plugin.description.settings ?? [];
  const [values, setValues] = useState<NonNullable<PluginInput['values']>>(() => {
    const initial = { ...plugin.configuration.values };
    for (const setting of settings) {
      if (!setting.secret && initial[setting.key] === undefined && setting.default !== undefined) initial[setting.key] = setting.default;
    }
    return initial;
  });
  const [secrets, setSecrets] = useState<Record<string, string>>({});
  const [projects, setProjects] = useState<Project[]>([]);
  const [catalog, setCatalog] = useState({ project: '', agents: [] as string[], models: [] as string[] });
  const slack = plugin.description.id === 'org.ocman.slack';
  // The catalog is tagged with the project it was fetched for, so options are
  // derived rather than cleared from an effect: a project with no answer yet
  // renders empty instead of briefly showing the previous project's lists.
  const project = slack && typeof values.project === 'string' ? values.project : '';
  // The installation's platform picks the catalog (AD-13); base ids, since the
  // owner resolves them against its own platforms.
  const platform = slack ? String(values.platform ?? '') : '';
  const platformOptions = useMachinePlatforms(plugin.ownerId).map((p) => ({ value: p.id.slice(p.id.indexOf(':') + 1), label: p.displayName }));
  const catalogKey = `${platform}\0${project}`;
  const options = project && catalog.project === catalogKey ? catalog : emptyCatalog;

  useEffect(() => {
    if (!slack) return;
    const controller = new AbortController();
    api.projects(controller.signal).then((items) => setProjects(items.filter((project) => (project.remoteId || 'local') === plugin.ownerId && !project.archived))).catch(() => {});
    return () => controller.abort();
  }, [plugin.ownerId, slack]);

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

  return <form className={styles.form} aria-label="Plugin configuration" onSubmit={(event) => {
    event.preventDefault();
    const input = { values, secrets };
    setSecrets({});
    void save(input);
  }}>
    {settings.map((s) => <SettingRow key={s.key} label={s.label} desc={s.secret ? 'Write-only. Leave blank to keep the current secret.' : s.required ? 'Required' : undefined}>
      {s.secret ? <SecretField wrapperClassName={styles.control} aria-label={s.label} autoComplete="new-password" value={secrets[s.key] ?? ''}
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
        })} /> : slack && s.key === 'project' ? <SearchSelect className={styles.control} ariaLabel={s.label} searchLabel="Search projects" placeholder="Select a project" value={String(values.project ?? '')}
        options={selectOptions(projects.map((project) => project.directory), String(values.project ?? ''), 'Select a project')}
        onChange={(project) => setValues((previous) => ({ ...previous, project, agent: '', model: '' }))} />
      : slack && s.key === 'platform' ? <SearchSelect className={styles.control} ariaLabel={s.label} searchLabel="Search platforms" placeholder="Default platform" value={platform}
        options={[{ value: '', label: 'Default platform' }, ...platformOptions,
          ...(platform && !platformOptions.some((option) => option.value === platform) ? [{ value: platform, label: platform }] : [])]}
        onChange={(next) => setValues((previous) => ({ ...previous, platform: next, agent: '', model: '' }))} />
      : slack && (s.key === 'agent' || s.key === 'model') ? <SearchSelect className={styles.control} ariaLabel={s.label} searchLabel={`Search ${s.key}s`} placeholder={`Default ${s.key}`} disabled={!values.project}
        value={String(values[s.key] ?? '')} options={selectOptions(s.key === 'agent' ? options.agents : options.models, String(values[s.key] ?? ''), `Default ${s.key}`)}
        onChange={(value) => selectValue(s.key, value)} />
      : s.type === 'boolean' ? <SelectField className={styles.control} aria-label={s.label} required={s.required} value={String(values[s.key] ?? '')} onChange={(event) => setValues((previous) => {
        const next = { ...previous };
        if (event.target.value === '') delete next[s.key]; else next[s.key] = event.target.value === 'true';
        return next;
      })}>
        <option value="">Not set</option><option value="true">Yes</option><option value="false">No</option>
      </SelectField> : s.enum?.length ? <SelectField className={styles.control} aria-label={s.label} required={s.required} value={String(values[s.key] ?? '')} onChange={(event) => setValues((previous) => {
        const next = { ...previous };
        if (event.target.value === '') delete next[s.key]; else next[s.key] = event.target.value;
        return next;
      })}>
        <option value="">Select a value</option>{s.enum.map((option) => <option key={option}>{option}</option>)}
      </SelectField> : <TextField className={styles.control} aria-label={s.label} type={s.type === 'string' ? 'text' : 'number'} step={s.type === 'integer' ? 1 : 'any'} required={s.required}
        value={String(values[s.key] ?? '')} onChange={(event) => setValues((previous) => {
          const next = { ...previous };
          if (s.type === 'string') next[s.key] = event.target.value;
          else if (event.target.value === '') delete next[s.key];
          else next[s.key] = Number(event.target.value);
          return next;
        })} />}
    </SettingRow>)}
    {!settings.length && <p>No configuration settings.</p>}
    <Button type="submit" variant="accent" className={styles.save}>Save configuration</Button>
  </form>;
}
