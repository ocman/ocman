import { useEffect, useState } from 'react';
import { api, type Project } from '../lib/api';
import type { PluginInput, PluginRegistration } from '../lib/plugins';
import { plugins } from '../lib/plugins';
import { SearchSelect } from './SearchSelect';
import { SettingRow } from './SettingRow';

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
  const options = project && catalog.project === project ? catalog : emptyCatalog;

  useEffect(() => {
    if (!slack) return;
    const controller = new AbortController();
    api.projects(controller.signal).then((items) => setProjects(items.filter((project) => (project.remoteId || 'local') === plugin.ownerId && !project.archived))).catch(() => {});
    return () => controller.abort();
  }, [plugin.ownerId, slack]);

  useEffect(() => {
    if (!project) return;
    const controller = new AbortController();
    plugins.projectCatalog(plugin.ownerId, project, controller.signal)
      .then((next) => setCatalog({ project, ...next }))
      .catch(() => setCatalog({ project, agents: [], models: [] }));
    return () => controller.abort();
  }, [plugin.ownerId, project]);

  const selectValue = (key: string, value: string) => setValues((previous) => ({ ...previous, [key]: value }));
  const selectOptions = (items: string[], current: string, empty: string) => [
    { value: '', label: empty },
    ...Array.from(new Set([...items, current].filter(Boolean))).map((value) => ({ value, label: value })),
  ];

  return <form aria-label="Plugin configuration" onSubmit={(event) => {
    event.preventDefault();
    const input = { values, secrets };
    setSecrets({});
    void save(input);
  }}>
    {settings.map((s) => <SettingRow key={s.key} label={s.label} desc={s.secret ? 'Write-only. Leave blank to keep the current secret.' : s.required ? 'Required' : undefined}>
      {s.secret ? <>
        <input aria-label={s.label} type="password" autoComplete="new-password" value={secrets[s.key] ?? ''}
          required={s.required && !plugin.configuration.secrets?.[s.key]}
          onChange={(event) => setSecrets((previous) => {
            const next = { ...previous };
            if (event.target.value) next[s.key] = event.target.value; else delete next[s.key];
            return next;
          })} />
        {!s.required && <label><input type="checkbox" checked={secrets[s.key] === ''} onChange={(event) => setSecrets((previous) => {
          const next = { ...previous };
          if (event.target.checked) next[s.key] = ''; else delete next[s.key];
          return next;
        })} />Clear {s.label}</label>}
      </> : slack && s.key === 'project' ? <SearchSelect ariaLabel={s.label} searchLabel="Search projects" placeholder="Select a project" value={String(values.project ?? '')}
        options={selectOptions(projects.map((project) => project.directory), String(values.project ?? ''), 'Select a project')}
        onChange={(project) => setValues((previous) => ({ ...previous, project, agent: '', model: '' }))} />
      : slack && (s.key === 'agent' || s.key === 'model') ? <SearchSelect ariaLabel={s.label} searchLabel={`Search ${s.key}s`} placeholder={`Default ${s.key}`} disabled={!values.project}
        value={String(values[s.key] ?? '')} options={selectOptions(s.key === 'agent' ? options.agents : options.models, String(values[s.key] ?? ''), `Default ${s.key}`)}
        onChange={(value) => selectValue(s.key, value)} />
      : s.type === 'boolean' ? <select aria-label={s.label} required={s.required} value={String(values[s.key] ?? '')} onChange={(event) => setValues((previous) => {
        const next = { ...previous };
        if (event.target.value === '') delete next[s.key]; else next[s.key] = event.target.value === 'true';
        return next;
      })}>
        <option value="">Not set</option><option value="true">Yes</option><option value="false">No</option>
      </select> : s.enum?.length ? <select aria-label={s.label} required={s.required} value={String(values[s.key] ?? '')} onChange={(event) => setValues((previous) => {
        const next = { ...previous };
        if (event.target.value === '') delete next[s.key]; else next[s.key] = event.target.value;
        return next;
      })}>
        <option value="">Select a value</option>{s.enum.map((option) => <option key={option}>{option}</option>)}
      </select> : <input aria-label={s.label} type={s.type === 'string' ? 'text' : 'number'} step={s.type === 'integer' ? 1 : 'any'} required={s.required}
        value={String(values[s.key] ?? '')} onChange={(event) => setValues((previous) => {
          const next = { ...previous };
          if (s.type === 'string') next[s.key] = event.target.value;
          else if (event.target.value === '') delete next[s.key];
          else next[s.key] = Number(event.target.value);
          return next;
        })} />}
    </SettingRow>)}
    {!settings.length && <p>No configuration settings.</p>}
    <button type="submit" className="vscode-btn">Save configuration</button>
  </form>;
}
