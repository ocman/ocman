import { useEffect, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '../components/Control';
import { Modal } from '../components/Modal';
import { ProjectLabel } from '../components/ProjectLabel';
import { SearchSelect } from '../components/SearchSelect';
import { api, type Project, type Routine, type RoutineInput, type RoutineRun, type RoutineScheduleKind, type RoutineSessionMode, type Session } from '../lib/api';
import { cleanTitle, formatDateTimeShort } from '../lib/format';
import { usePageTitle } from '../lib/headerContext';
import './Routines.css';

const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';

type FormState = {
  name: string;
  prompt: string;
  directory: string;
  agent: string;
  model: string;
  sessionMode: RoutineSessionMode;
  sessionId: string;
  remoteId: string;
  kind: RoutineScheduleKind;
  timeoutMinutes: string;
  at: string;
  cron: string;
  timezone: string;
  enabled: boolean;
  deleteAfterSuccess: boolean;
};

const emptyForm = (): FormState => ({
  name: '', prompt: '', directory: '', agent: '', model: '', sessionMode: 'new', sessionId: '', remoteId: '', kind: 'none', timeoutMinutes: '30', at: '', cron: '', timezone,
  enabled: true, deleteAfterSuccess: false,
});

function formFor(routine: Routine): FormState {
  const config = JSON.parse(routine.scheduleConfigJSON || '{}') as { at?: number; cron?: string; timezone?: string };
  return {
    name: routine.name,
    prompt: routine.prompt,
    directory: routine.directory,
    agent: routine.agent,
    model: routine.model,
    sessionMode: routine.sessionMode,
    sessionId: routine.sessionId,
    remoteId: routine.remoteId,
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
    agent: form.agent,
    model: form.model,
    sessionMode: form.sessionMode,
    sessionId: form.sessionId,
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

function sessionKey(remoteId: string, sessionId: string) {
  return `${encodeURIComponent(remoteId || 'local')}:${encodeURIComponent(sessionId)}`;
}

export function Routines() {
  usePageTitle('Routines');
  const [routines, setRoutines] = useState<Routine[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [catalog, setCatalog] = useState({ agents: [] as string[], models: [] as string[] });
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [history, setHistory] = useState<Record<string, RoutineRun[]>>({});
  const [form, setForm] = useState<FormState>(emptyForm);
  const [editing, setEditing] = useState<string>();
  const [historyRoutine, setHistoryRoutine] = useState<Routine>();
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

  useEffect(() => {
    if (!showForm || !form.directory) {
      setSessions([]);
      return;
    }
    let active = true;
    setSessionsLoading(true);
    api.sessions({ dir: form.directory }).then(
      (items) => { if (active) setSessions(items ?? []); },
      (err) => { if (active) setError(err instanceof Error ? err.message : 'Could not load sessions.'); },
    ).finally(() => { if (active) setSessionsLoading(false); });
    return () => { active = false; };
  }, [form.directory, showForm]);

  useEffect(() => {
    const source = sessions.find((session) => session.directory === form.directory && (session.remoteId || 'local') === (form.remoteId || 'local') && !session.parentId && !session.stale);
    if (!showForm || !source) {
      setCatalog({ agents: [], models: [] });
      return;
    }
    let active = true;
    setCatalogLoading(true);
    Promise.all([api.agents(source.id, undefined, source.platform), api.sessionModels(source.id, source.platform)]).then(
      ([agents, models]) => { if (active) setCatalog({ agents: agents.map((agent) => agent.name), models: models.models.filter((model) => model.isAvailable !== false).map((model) => `${model.provider}/${model.model}`) }); },
      () => { if (active) setCatalog({ agents: [], models: [] }); },
    ).finally(() => { if (active) setCatalogLoading(false); });
    return () => { active = false; };
  }, [form.directory, form.remoteId, sessions, showForm]);

  const openCreate = () => {
    setHistoryRoutine(undefined);
    setEditing(undefined);
    setForm(emptyForm());
    setShowForm(true);
    setError('');
  };

  const openEdit = (routine: Routine) => {
    setHistoryRoutine(undefined);
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
      const remoteId = form.remoteId || 'local';
      const input = inputFor(form, remoteId);
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

  const availableProjects = projects.filter((project) => !project.archived);
  const projectOptions = Array.from(new Map(availableProjects.map((project) => {
    const remoteId = project.remoteId || 'local';
    return [sessionKey(remoteId, project.directory), { value: sessionKey(remoteId, project.directory), label: `${project.directory}${project.remoteName ? ` · ${project.remoteName}` : ''}` }];
  })).values());
  const selectedProjectKey = form.directory ? sessionKey(form.remoteId, form.directory) : '';
  if (selectedProjectKey && !projectOptions.some((option) => option.value === selectedProjectKey)) {
    projectOptions.unshift({ value: selectedProjectKey, label: form.directory });
  }
  const availableSessions = sessions.filter((session) => session.directory === form.directory && (session.remoteId || 'local') === (form.remoteId || 'local') && !session.parentId && !session.archived && !session.stale);
  const sessionOptions = availableSessions.map((session) => ({
    value: sessionKey(session.remoteId || 'local', session.id),
    label: `${cleanTitle(session.title) || session.id}${session.remoteName ? ` · ${session.remoteName}` : ''}`,
  }));
  const selectedSessionKey = form.sessionId ? sessionKey(form.remoteId, form.sessionId) : '';
  if (selectedSessionKey && !sessionOptions.some((option) => option.value === selectedSessionKey)) {
    sessionOptions.unshift({ value: selectedSessionKey, label: form.sessionId });
  }
  const agentOptions = ['', ...new Set([...catalog.agents, form.agent].filter(Boolean))].map((agent) => ({ value: agent, label: agent || 'Default agent' }));
  const modelOptions = ['', ...new Set([...catalog.models, form.model].filter(Boolean))].map((model) => ({ value: model, label: model || 'Default model' }));
  const selectedRuns = historyRoutine ? history[historyRoutine.id] ?? [] : [];

  return (
    <main className="routine-page">
      <header className="routine-header">
        <p>Save a prompt, run it now, or schedule it for later.</p>
        <Button type="button" variant="accent" onClick={openCreate}><i className="bi bi-plus-lg" aria-hidden="true" />New routine</Button>
      </header>

      {error && !showForm && <p role="alert" className="routine-error">{error}</p>}

      {showForm && (
        <Modal label={editing ? 'Edit routine' : 'New routine'} onClose={() => setShowForm(false)} canClose={!busy} backdropClassName="routine-drawer-backdrop" dialogClassName="routine-drawer" backdropTestId="routine-drawer-backdrop">
        <form className="routine-form" onSubmit={submit}>
          <header><h2>{editing ? 'Edit routine' : 'New routine'}</h2><button type="button" disabled={busy} onClick={() => setShowForm(false)} aria-label="Close routine form" title="Close"><i className="bi bi-x-lg" aria-hidden="true" /></button></header>
          {error && <p role="alert" className="routine-error">{error}</p>}
          <label>Name<input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></label>
          <label>Prompt<textarea required value={form.prompt} onChange={(event) => setForm({ ...form, prompt: event.target.value })} /></label>
          <label>Project
            <SearchSelect value={selectedProjectKey} options={projectOptions} ariaLabel="Project" placeholder="Select a project" searchLabel="Search projects" onChange={(key) => {
              const project = availableProjects.find((item) => sessionKey(item.remoteId || 'local', item.directory) === key);
              if (project) setForm({ ...form, directory: project.directory, remoteId: project.remoteId || 'local', sessionId: '', agent: '', model: '' });
            }} />
          </label>
          <label>Session<select aria-label="Session" value={form.sessionMode} onChange={(event) => setForm({ ...form, sessionMode: event.target.value as RoutineSessionMode, sessionId: '' })}>
            <option value="new">New session</option><option value="reuse">Reuse session</option><option value="existing">Existing session</option>
          </select><small>{form.sessionMode === 'new' ? 'Create a fresh session for every run.' : form.sessionMode === 'reuse' ? 'Create one on the first run, then keep using it.' : 'Continue a session from this project.'}</small></label>
          {form.sessionMode === 'existing' && <label>Existing session
            <SearchSelect value={selectedSessionKey} options={sessionOptions} ariaLabel="Existing session" placeholder={sessionsLoading ? 'Loading sessions...' : 'Select a session'} searchLabel="Search sessions" disabled={sessionsLoading || !form.directory} onChange={(key) => {
              const session = availableSessions.find((item) => sessionKey(item.remoteId || 'local', item.id) === key);
              if (session) setForm({ ...form, sessionId: session.id, remoteId: session.remoteId || 'local' });
            }} />
          </label>}
          <label>Agent<SearchSelect value={form.agent} options={agentOptions} ariaLabel="Agent" placeholder="Default agent" searchLabel="Search agents" disabled={catalogLoading || !form.directory} onChange={(agent) => setForm({ ...form, agent })} /></label>
          <label>Model<SearchSelect value={form.model} options={modelOptions} ariaLabel="Model" placeholder="Default model" searchLabel="Search models" disabled={catalogLoading || !form.directory} onChange={(model) => setForm({ ...form, model })} /></label>
          <label>Schedule<select value={form.kind} onChange={(event) => setForm({ ...form, kind: event.target.value as RoutineScheduleKind })}>
            <option value="none">None</option><option value="timeout">Timeout</option><option value="once">Once</option><option value="cron">Cron</option>
          </select></label>
          {form.kind === 'timeout' && <label>Minutes from now<input required min="1" type="number" value={form.timeoutMinutes} onChange={(event) => setForm({ ...form, timeoutMinutes: event.target.value })} /></label>}
          {form.kind === 'once' && <label>Run at<input required type="datetime-local" value={form.at} onChange={(event) => setForm({ ...form, at: event.target.value })} /></label>}
          {form.kind === 'cron' && <><label>Cron expression<input required placeholder="0 9 * * *" value={form.cron} onChange={(event) => setForm({ ...form, cron: event.target.value })} /></label><label>Timezone<input required value={form.timezone} onChange={(event) => setForm({ ...form, timezone: event.target.value })} /></label></>}
          <label className="routine-check"><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} /> Enabled</label>
          <label className="routine-check"><input type="checkbox" checked={form.deleteAfterSuccess} onChange={(event) => setForm({ ...form, deleteAfterSuccess: event.target.checked })} /> Delete after a successful run</label>
          <div className="routine-actions"><Button disabled={busy || !form.directory || (form.sessionMode === 'existing' && !form.sessionId)} type="submit" variant="accent">{editing ? 'Save changes' : 'Create routine'}</Button><Button type="button" disabled={busy} onClick={() => setShowForm(false)}>Cancel</Button></div>
        </form>
        </Modal>
      )}

      {historyRoutine && (
        <Modal label={`${historyRoutine.name} history`} onClose={() => setHistoryRoutine(undefined)} backdropClassName="routine-drawer-backdrop" dialogClassName="routine-drawer" backdropTestId="routine-drawer-backdrop">
          <div className="routine-form">
            <header><h2>{historyRoutine.name}</h2><button type="button" onClick={() => setHistoryRoutine(undefined)} aria-label="Close routine history" title="Close"><i className="bi bi-x-lg" aria-hidden="true" /></button></header>
            <section className="routine-detail-history" aria-labelledby="routine-history-heading"><h3 id="routine-history-heading">History</h3>{selectedRuns.length === 0 ? <p className="oc-empty">No runs yet.</p> : <div className="routine-history-table-wrap"><table><thead><tr><th>Started</th><th>Trigger</th><th>Status</th><th>Session</th></tr></thead><tbody>{selectedRuns.map((run) => <tr key={run.id}><td>{formatDateTimeShort(run.startedAt || run.createdAt)}</td><td>{run.trigger}</td><td><span className={`routine-state ${run.state}`}>{run.state}</span>{run.error && <small className="routine-error">{run.error}</small>}</td><td>{run.sessionId ? <Link to={`/session/${encodeURIComponent(run.sessionId)}?platform=${encodeURIComponent(run.platform ?? '')}`}>Open</Link> : '-'}</td></tr>)}</tbody></table></div>}</section>
          </div>
        </Modal>
      )}

      {loading ? <div className="oc-list-loading" role="status"><div className="oc-spinner" />Loading routines...</div> : routines.length === 0 ? <p className="oc-empty">No routines yet.</p> : (
        <section className="routine-list" aria-label="Saved routines"><div className="routine-table-wrap"><table><thead><tr><th>Name</th><th>Project</th><th>Session</th><th>Schedule</th><th>Next run</th><th>Status</th><th>Actions</th></tr></thead><tbody>{routines.map((routine) => {
          const latest = history[routine.id]?.[0];
          const status = routine.expiredAt && routine.expiredAt > (latest?.createdAt ?? 0) ? 'expired' : latest?.state ?? (routine.enabled ? 'ready' : 'disabled');
          return <tr key={routine.id} tabIndex={0} aria-label={`View ${routine.name} history`} onClick={() => setHistoryRoutine(routine)} onKeyDown={(event) => { if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) { event.preventDefault(); setHistoryRoutine(routine); } }}>
            <td><strong>{routine.name}</strong><small>{routine.prompt}</small></td><td><ProjectLabel path={routine.directory} /></td><td>{routine.sessionMode === 'new' ? 'New each run' : routine.sessionMode === 'reuse' ? 'Reuse' : 'Existing'}</td><td>{routine.scheduleKind}</td><td>{routine.nextDueAt ? formatDateTimeShort(routine.nextDueAt) : '-'}</td><td><span className={`routine-state ${status}`}>{status}</span></td><td><div className="routine-actions routine-row-actions"><Button aria-label="Run" title="Run" size="small" disabled={busy} type="button" variant="accent" onClick={(event) => { event.stopPropagation(); void act(() => api.routines.run(routine.id)); }}><i className="bi bi-play-fill" aria-hidden="true" /></Button><Button aria-label="Edit" title="Edit" size="small" disabled={busy} type="button" onClick={(event) => { event.stopPropagation(); openEdit(routine); }}><i className="bi bi-pencil" aria-hidden="true" /></Button><Button aria-label="Delete" title="Delete" size="small" disabled={busy} type="button" className="routine-delete" onClick={(event) => { event.stopPropagation(); if (window.confirm(`Delete "${routine.name}"?`)) void act(() => api.routines.remove(routine.id)); }}><i className="bi bi-trash" aria-hidden="true" /></Button></div></td>
          </tr>;
        })}</tbody></table></div></section>
      )}
    </main>
  );
}
