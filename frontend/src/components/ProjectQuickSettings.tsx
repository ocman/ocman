import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { api, postJSON } from '../lib/api';
import { clearSettingsCache, loadProjectSettings, type ProjectDefaults } from '../lib/projectSettingsCache';
import { Button, SelectField } from './Control';
import { SearchSelect, type SearchSelectOption } from './SearchSelect';
import { SettingRow } from './SettingRow';
import { Popover } from './Popover';
import './ProjectQuickSettings.css';

export function ProjectQuickSettings({ directory, remoteId = 'local', children, className, title }: {
  directory: string; remoteId?: string; children: ReactNode; className?: string; title?: string;
}) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({ top: 0, left: 12 });
  const trigger = useRef<HTMLButtonElement>(null);
  const id = useId();
  return <span className="project-quick-settings">
    <Button ref={trigger} type="button" variant="ghost" size="compact" className={className} title={title}
      aria-label="Project quick settings" aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? id : undefined}
      onClick={(event) => {
        const anchor = event.currentTarget.getBoundingClientRect();
        setPosition({ top: anchor.bottom + 8, left: Math.max(12, Math.min(anchor.left, window.innerWidth - 352)) });
        setOpen(!open);
      }}>{children}</Button>
    <Popover open={open} onClose={() => setOpen(false)} triggerRef={trigger} id={id} label="Project quick settings" className="project-quick-settings-popover" style={position}>
      <ProjectDefaultsForm key={JSON.stringify([directory, remoteId])} directory={directory} remoteId={remoteId} onClose={() => { setOpen(false); trigger.current?.focus(); }} />
    </Popover>
  </span>;
}

function ProjectDefaultsForm({ directory, remoteId, onClose }: { directory: string; remoteId: string; onClose: () => void }) {
  const [defaults, setDefaults] = useState<ProjectDefaults>();
  const [models, setModels] = useState<SearchSelectOption[]>([]);
  const [agents, setAgents] = useState<SearchSelectOption[]>([]);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const mounted = useRef(false);
  const id = useId();
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setError('');
    Promise.all([loadProjectSettings(directory, remoteId), api.prepareSession({ directory, remoteId }, controller.signal)])
      .then(([settings, catalog]) => {
        if (controller.signal.aborted) return;
        setDefaults(settings.defaults || { model: '', agent: '', worktree: '' });
        setModels(catalog.models.models.map((m) => ({ value: `${m.provider}/${m.model}`, label: `${m.providerName || m.provider} / ${m.modelName || m.model}` })));
        setAgents(catalog.agents.filter((a) => a.mode !== 'subagent' && !a.hidden).map((a) => ({ value: a.name, label: a.name })));
      }).catch((err: unknown) => { if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err)); });
    return () => controller.abort();
  }, [directory, remoteId, attempt]);
  const inherited = { value: '', label: 'Use inherited default' };
  const options = (choices: SearchSelectOption[], current: string) => [inherited, ...(current && !choices.some((o) => o.value === current) ? [{ value: current, label: current }] : []), ...choices];
  return <form onSubmit={async (event) => {
    event.preventDefault();
    if (!defaults || saving) return;
    setSaving(true); setError('');
    try {
      await postJSON('/api/project/settings', { directory, remoteId, defaults });
      clearSettingsCache();
      if (mounted.current) onClose();
    } catch (err) { if (mounted.current) setError(err instanceof Error ? err.message : String(err)); }
    finally { if (mounted.current) setSaving(false); }
  }}>
    <h2>Project defaults</h2>
    <p>Used for new conversations on this machine. Individual composer choices take precedence.</p>
    {error && <p role="alert">{error}</p>}
    {!defaults ? <>{!error ? <p role="status">Loading…</p> : <Button type="button" onClick={() => setAttempt(attempt + 1)}>Retry</Button>}</> : <>
      <fieldset disabled={saving}>
        <SettingRow block label="Default model">
          <SearchSelect value={defaults.model} options={options(models, defaults.model)} ariaLabel="Default model" placeholder="Use inherited default" searchLabel="Search models" disabled={saving} onChange={(model) => setDefaults({ ...defaults, model })} />
        </SettingRow>
        <SettingRow block label="Default agent">
          <SearchSelect value={defaults.agent} options={options(agents, defaults.agent)} ariaLabel="Default agent" placeholder="Use inherited default" searchLabel="Search agents" disabled={saving} onChange={(agent) => setDefaults({ ...defaults, agent })} />
        </SettingRow>
        <SettingRow block label={<label htmlFor={`${id}-worktree`}>Default worktree behavior</label>}>
          <SelectField id={`${id}-worktree`} value={defaults.worktree} onChange={(event) => setDefaults({ ...defaults, worktree: event.target.value as ProjectDefaults['worktree'] })}>
            <option value="">Use inherited default</option><option value="worktree">New worktree when available</option><option value="current">Current checkout</option>
          </SelectField>
        </SettingRow>
      </fieldset>
      <div className="project-quick-settings-actions">
        <Button type="button" variant="ghost" size="small" disabled={saving} onClick={onClose}>Cancel</Button>
        <Button type="submit" variant="accent" size="small" disabled={saving} aria-busy={saving}>{saving ? 'Saving…' : 'Save'}</Button>
      </div>
    </>}
  </form>;
}
