import { useEffect, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { SearchSelect } from '../components/SearchSelect';
import { api, type Project, type Routine, type RoutineInput, type RoutineRun, type RoutineScheduleKind } from '../lib/api';
import { formatDateTimeShort } from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import { resolveTargetForDir } from '../lib/machinePicker';
import './Routines.css';

const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';

type FormState = {
  name: string;
  prompt: string;
  directory: string;
  kind: RoutineScheduleKind;
  timeoutMinutes: string;
  at: string;
  cron: string;
  timezone: string;
  enabled: boolean;
  deleteAfterSuccess: boolean;
};

const emptyForm = (): FormState => ({
  name: '', prompt: '', directory: '', kind: 'none', timeoutMinutes: '30', at: '', cron: '', timezone,
  enabled: true, deleteAfterSuccess: false,
});

function formFor(routine: Routine): FormState {
  const config = JSON.parse(routine.scheduleConfigJSON || '{}') as { at?: number; cron?: string; timezone?: string };
  return {
    name: routine.name,
    prompt: routine.prompt,
    directory: routine.directory,
    kind: routine.scheduleKind,
    timeoutMinutes: String(Math.max(1, Math.round((routine.nextDueAt - routine.updatedAt) / 60_000))),
    at: config.at ? new Date(config.at - new Date(config.at).getTimezoneOffset() * 60_000).toISOString().slice(0, 16) : '',
    cron: config.cron ?? '',
    timezone: config.timezone ?? timezone,
    enabled: routine.enabled,
    deleteAfterSuccess: routine.deleteAfterSuccess,
  };
}

function inputFor(form: FormState, remoteId: string): RoutineInput {
  return {
    name: form.name,
    prompt: form.prompt,
    directory: form.directory,
    remoteId,
    schedule: {
      kind: form.kind,
      ...(form.kind === 'timeout' ? { timeoutMs: Number(form.timeoutMinutes) * 60_000 } : {}),
      ...(form.kind === 'once' ? { at: new Date(form.at).getTime() } : {}),
      ...(form.kind === 'cron' ? { cron: form.cron, timezone: form.timezone } : {}),
    },
    enabled: form.enabled,
    deleteAfterSuccess: form.deleteAfterSuccess,
  };
}

export function Routines() {
  usePageTitle('Routines');
  const [routines, setRoutines] = useState<Routine[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [history, setHistory] = useState<Record<string, RoutineRun[]>>({});
  const [form, setForm] = useState<FormState>(emptyForm);
  const [editing, setEditing] = useState<string>();
  const [showForm, setShowForm] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const load = async () => {
    const [items, projectItems] = await Promise.all([api.routines.list(), api.projects()]);
    setRoutines(items);
    setProjects(projectItems);
    const entries = await Promise.all(items.map(async (item) => [item.id, await api.routines.history(item.id)] as const));
    setHistory(Object.fromEntries(entries));
  };

  useEffect(() => {
    let active = true;
    let refreshing = false;
    const refresh = async () => {
      if (refreshing) return;
      refreshing = true;
      try {
        const [items, projectItems] = await Promise.all([api.routines.list(), api.projects()]);
        const entries = await Promise.all(items.map(async (item) => [item.id, await api.routines.history(item.id)] as const));
        if (active) {
          setRoutines(items);
          setProjects(projectItems);
          setHistory(Object.fromEntries(entries));
        }
      } catch (err) {
        if (active) setError(err instanceof Error ? err.message : 'Could not load routines.');
      } finally {
        refreshing = false;
        if (active) setLoading(false);
      }
    };
    void refresh();
    const interval = window.setInterval(() => void refresh(), 5_000);
    return () => { active = false; window.clearInterval(interval); };
  }, []);

  const openCreate = () => {
    setEditing(undefined);
    setForm(emptyForm());
    setShowForm(true);
    setError('');
  };

  const openEdit = (routine: Routine) => {
    setEditing(routine.id);
    setForm(formFor(routine));
    setShowForm(true);
    setError('');
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      const target = await resolveTargetForDir(form.directory);
      if (!target) return;
      if (!target.remoteId) throw new Error('Could not resolve routine target.');
      const input = inputFor(form, target.remoteId);
      if (editing) await api.routines.update(editing, input);
      else await api.routines.create(input);
      await load();
      setShowForm(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save routine.');
    } finally {
      setBusy(false);
    }
  };

  const act = async (action: () => Promise<unknown>) => {
    setBusy(true);
    setError('');
    try {
      await action();
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Routine action failed.');
    } finally {
      setBusy(false);
    }
  };

  const projectOptions = Array.from(new Set(projects.filter((project) => !project.archived).map((project) => project.directory)))
    .map((directory) => ({ value: directory, label: directory }));

  return (
    <main className="routine-page">
      <header className="routine-header">
        <div><span className="routine-kicker">Automation</span><h1>Routines</h1><p>Save a prompt, run it now, or schedule it for later.</p></div>
        <button type="button" onClick={openCreate}>New routine</button>
      </header>

      {error && <p role="alert" className="routine-error">{error}</p>}

      {showForm && (
        <form className="routine-form" onSubmit={submit}>
          <h2>{editing ? 'Edit routine' : 'New routine'}</h2>
          <label>Name<input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></label>
          <label>Prompt<textarea required value={form.prompt} onChange={(event) => setForm({ ...form, prompt: event.target.value })} /></label>
          <label>Project
            <SearchSelect value={form.directory} options={projectOptions} ariaLabel="Project" placeholder="Select a project" searchLabel="Search projects" onChange={(directory) => setForm({ ...form, directory })} />
          </label>
          <label>Schedule<select value={form.kind} onChange={(event) => setForm({ ...form, kind: event.target.value as RoutineScheduleKind })}>
            <option value="none">None</option><option value="timeout">Timeout</option><option value="once">Once</option><option value="cron">Cron</option>
          </select></label>
          {form.kind === 'timeout' && <label>Minutes from now<input required min="1" type="number" value={form.timeoutMinutes} onChange={(event) => setForm({ ...form, timeoutMinutes: event.target.value })} /></label>}
          {form.kind === 'once' && <label>Run at<input required type="datetime-local" value={form.at} onChange={(event) => setForm({ ...form, at: event.target.value })} /></label>}
          {form.kind === 'cron' && <><label>Cron expression<input required placeholder="0 9 * * *" value={form.cron} onChange={(event) => setForm({ ...form, cron: event.target.value })} /></label><label>Timezone<input required value={form.timezone} onChange={(event) => setForm({ ...form, timezone: event.target.value })} /></label></>}
          <label className="routine-check"><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} /> Enabled</label>
          <label className="routine-check"><input type="checkbox" checked={form.deleteAfterSuccess} onChange={(event) => setForm({ ...form, deleteAfterSuccess: event.target.checked })} /> Delete after a successful run</label>
          <div className="routine-actions"><button disabled={busy || !form.directory} type="submit">{editing ? 'Save changes' : 'Create routine'}</button><button type="button" onClick={() => setShowForm(false)}>Cancel</button></div>
        </form>
      )}

      {loading ? <p role="status">Loading routines...</p> : routines.length === 0 ? <p className="routine-empty">No routines yet.</p> : (
        <section className="routine-list" aria-label="Saved routines">
          {routines.map((routine) => {
            const runs = history[routine.id] ?? [];
            const latest = runs[0];
            return <article key={routine.id}>
              <header><div><h2>{routine.name}</h2><p>{routine.directory}</p></div><span className={`routine-state ${latest?.state ?? ''}`}>{latest?.state ?? (routine.enabled ? 'ready' : 'disabled')}</span></header>
              <p className="routine-prompt">{routine.prompt}</p>
              <dl><div><dt>Schedule</dt><dd>{routine.scheduleKind}</dd></div><div><dt>Next run</dt><dd>{routine.nextDueAt ? formatDateTimeShort(routine.nextDueAt) : 'Not scheduled'}</dd></div></dl>
              {latest?.error && <p role="alert" className="routine-error">{latest.error}</p>}
              <div className="routine-actions"><button disabled={busy} type="button" onClick={() => void act(() => api.routines.run(routine.id))}>Run now</button><button disabled={busy} type="button" onClick={() => openEdit(routine)}>Edit</button><button disabled={busy} type="button" className="routine-delete" onClick={() => void act(() => api.routines.remove(routine.id))}>Delete</button></div>
              {runs.length > 0 && <details><summary>History ({runs.length})</summary><ul className="routine-history">{runs.map((run) => <li key={run.id}><span>{formatDateTimeShort(run.createdAt)} · {run.trigger} · {run.state}</span>{run.sessionId && <Link to={`/session/${encodeURIComponent(run.sessionId)}?platform=${encodeURIComponent(run.platform ?? '')}`}>Open session</Link>}{run.error && <span className="routine-error">{run.error}</span>}</li>)}</ul></details>}
            </article>;
          })}
        </section>
      )}
    </main>
  );
}
