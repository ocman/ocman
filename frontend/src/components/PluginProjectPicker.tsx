import { Button } from './Control';
import { ProjectScopePicker } from './ProjectScopePicker';
import { useProjects } from '../lib/queries';
import { shortPath } from '../lib/format';

export function PluginProjectPicker({ ownerId, label, value, onChange }: {
  ownerId: string; label: string; value: string; onChange: (value: string) => void;
}) {
  const projects = useProjects();
  const selected = value.split('\n').map((p) => p.trim()).filter(Boolean);
  const choices = (projects.data ?? []).filter((p) => (p.remoteId || 'local') === ownerId);
  return <div className="plugin-project-picker">
    {selected.length === 0 && <small>No code projects selected.</small>}
    <ul aria-label={`${label} selected projects`}>{selected.map((directory) => <li key={directory}>
      <span title={directory}>{shortPath(directory)}</span>
      {!choices.some((p) => p.directory === directory) && <small>Unavailable on this owner</small>}
      <Button variant="ghost" aria-label={`Remove ${directory}`} onClick={() => onChange(selected.filter((p) => p !== directory).join('\n'))}>Remove</Button>
    </li>)}</ul>
    <ProjectScopePicker projects={choices.filter((p) => !selected.includes(p.directory))} value=""
      onlyProjects label={label} placeholder="Add a project…" onChange={(directory) => {
        if (choices.some((p) => p.directory === directory) && !selected.includes(directory)) onChange([...selected, directory].join('\n'));
      }} />
    {projects.isError && <p role="alert">Could not load projects. Existing selections are preserved.</p>}
  </div>;
}
