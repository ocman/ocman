import { useEffect, useState, type FormEvent } from 'react';
import { Button, SelectField, TextField, TextareaField } from '../components/Control';
import { CheckboxField } from '../components/CheckboxField';
import { Drawer } from '../components/Drawer';
import { InlineAlert } from '../components/InlineAlert';
import { ModalFooter } from '../components/ModalFooter';
import { SearchSelect } from '../components/SearchSelect';
import { PermissionRulesEditor } from '../components/PermissionRulesEditor';
import { WebhookTriggerFields } from '../components/WebhookTriggerFields';
import { api, type PermissionRule, type Project, type Routine, type RoutineInput, type RoutineScheduleKind, type RoutineSessionMode, type Session } from '../lib/api';
import type { WebhookInbox } from '../lib/api.types';
import { saveTrigger, triggerFor, type Trigger } from '../lib/webhookFilters';
import { cleanTitle } from '../lib/format';
import styles from './RoutineEditorDrawer.module.css';

const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
type FormState = {
  name: string; prompt: string; directory: string; agent: string; model: string;
  sessionMode: RoutineSessionMode; sessionId: string; remoteId: string;
  kind: RoutineScheduleKind; timeoutMinutes: string; at: string; cron: string; timezone: string;
  enabled: boolean; deleteAfterSuccess: boolean; archiveSessionAfterSuccess: boolean; notifyOnSuccess: boolean;
};

const emptyForm = (): FormState => ({
  name: '', prompt: '', directory: '', agent: '', model: '', sessionMode: 'new', sessionId: '', remoteId: '', kind: 'none', timeoutMinutes: '30', at: '', cron: '', timezone,
  enabled: true, deleteAfterSuccess: false, archiveSessionAfterSuccess: false, notifyOnSuccess: false,
});

function formFor(routine: Routine): FormState {
  const config = JSON.parse(routine.scheduleConfigJSON || '{}') as { at?: number; cron?: string; timezone?: string };
  return {
    name: routine.name, prompt: routine.prompt, directory: routine.directory, agent: routine.agent, model: routine.model,
    sessionMode: routine.sessionMode, sessionId: routine.sessionId, remoteId: routine.remoteId, kind: routine.scheduleKind,
    timeoutMinutes: String(Math.max(1, Math.round((routine.nextDueAt - routine.updatedAt) / 60_000))),
    at: config.at ? new Date(config.at - new Date(config.at).getTimezoneOffset() * 60_000).toISOString().slice(0, 16) : '',
    cron: config.cron ?? '', timezone: config.timezone ?? timezone, enabled: routine.enabled,
    deleteAfterSuccess: routine.deleteAfterSuccess, archiveSessionAfterSuccess: routine.archiveSessionAfterSuccess, notifyOnSuccess: routine.notifyOnSuccess,
  };
}

function inputFor(form: FormState, permissionRules: PermissionRule[]): RoutineInput {
  return {
    name: form.name, prompt: form.prompt, directory: form.directory, remoteId: form.remoteId || 'local', agent: form.agent, model: form.model,
    sessionMode: form.sessionMode, sessionId: form.sessionId,
    schedule: {
      kind: form.kind,
      ...(form.kind === 'timeout' ? { timeoutMs: Number(form.timeoutMinutes) * 60_000 } : {}),
      ...(form.kind === 'once' ? { at: new Date(form.at).getTime() } : {}),
      ...(form.kind === 'cron' ? { cron: form.cron, timezone: form.timezone } : {}),
    },
    enabled: form.enabled, deleteAfterSuccess: form.deleteAfterSuccess, archiveSessionAfterSuccess: form.archiveSessionAfterSuccess,
    notifyOnSuccess: form.notifyOnSuccess, permissionRules,
  };
}

function parsePermissionRules(json: string): PermissionRule[] {
  try { return JSON.parse(json) as PermissionRule[]; } catch { return []; }
}

function sessionKey(remoteId: string, id: string) {
  return `${encodeURIComponent(remoteId || 'local')}:${encodeURIComponent(id)}`;
}

type EditorProps = {
  routine?: Routine; inboxes: WebhookInbox[]; onClose: () => void; onSaved: () => void; onRefresh: () => Promise<void>;
  onBusyChange?: (busy: boolean) => void;
};

export function RoutineEditorDrawer(props: EditorProps) {
  const [busy, setBusy] = useState(false);
  return <Drawer title="New routine" onClose={props.onClose} canClose={!busy} closeLabel="Close routine form" backdropTestId="routine-drawer-backdrop">
    <RoutineEditorForm {...props} onBusyChange={setBusy} />
  </Drawer>;
}

export function RoutineEditorForm({ routine, inboxes, onClose, onSaved, onRefresh, onBusyChange }: EditorProps) {
  const [form, setForm] = useState<FormState>(() => routine ? formFor(routine) : emptyForm());
  const [editing, setEditing] = useState(routine?.id);
  const [rules, setRules] = useState(() => routine ? parsePermissionRules(routine.permissionRulesJSON) : []);
  const [trigger, setTrigger] = useState<Trigger>(() => triggerFor(routine?.id, inboxes));
  const [projects, setProjects] = useState<Project[]>([]);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [catalog, setCatalog] = useState({ agents: [] as string[], models: [] as string[] });
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => { onBusyChange?.(busy); }, [busy, onBusyChange]);

  useEffect(() => {
    let active = true;
    api.projects().then((items) => { if (active) setProjects(items); }, (err) => { if (active) setError(err instanceof Error ? err.message : 'Could not load projects.'); });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    let active = true;
    const load = () => {
      if (!form.directory) { setSessions([]); return; }
      setSessionsLoading(true);
      api.sessions({ dir: form.directory }).then(
        (items) => { if (active) setSessions(items ?? []); },
        (err) => { if (active) setError(err instanceof Error ? err.message : 'Could not load sessions.'); },
      ).finally(() => { if (active) setSessionsLoading(false); });
    };
    load();
    return () => { active = false; };
  }, [form.directory]);

  useEffect(() => {
    let active = true;
    const load = () => {
      const source = sessions.find((session) => session.directory === form.directory && (session.remoteId || 'local') === (form.remoteId || 'local') && !session.parentId && !session.stale);
      if (!source) { setCatalog({ agents: [], models: [] }); return; }
      setCatalogLoading(true);
      Promise.all([api.agents(source.id, undefined, source.platform), api.sessionModels(source.id, source.platform)]).then(
        ([agents, models]) => { if (active) setCatalog({ agents: agents.map((agent) => agent.name), models: models.models.filter((model) => !models.hasProviders || model.isAvailable !== false).map((model) => `${model.provider}/${model.model}`) }); },
        () => { if (active) setCatalog({ agents: [], models: [] }); },
      ).finally(() => { if (active) setCatalogLoading(false); });
    };
    load();
    return () => { active = false; };
  }, [form.directory, form.remoteId, sessions]);

  const submit = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError('');
    let created = false;
    try {
      const input = inputFor(form, rules);
      const saved = editing ? await api.routines.update(editing, input) : await api.routines.create(input);
      if (!editing) { setEditing(saved.id); created = true; }
      await saveTrigger(saved.id, trigger, inboxes);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save routine.'); setBusy(false);
      // A created routine remains claimed when trigger saving fails, so retry updates it.
      if (created) void onRefresh().catch(() => undefined);
      return;
    }
    setBusy(false);
    onSaved();
  };

  const availableProjects = projects.filter((project) => !project.archived);
  const projectOptions = Array.from(new Map(availableProjects.map((project) => [sessionKey(project.remoteId || 'local', project.directory), {
    value: sessionKey(project.remoteId || 'local', project.directory), label: `${project.directory}${project.remoteName ? ` · ${project.remoteName}` : ''}`,
  }])).values());
  const selectedProjectKey = form.directory ? sessionKey(form.remoteId, form.directory) : '';
  if (selectedProjectKey && !projectOptions.some((option) => option.value === selectedProjectKey)) projectOptions.unshift({ value: selectedProjectKey, label: form.directory });
  const availableSessions = sessions.filter((session) => session.directory === form.directory && (session.remoteId || 'local') === (form.remoteId || 'local') && !session.parentId && !session.archived && !session.stale);
  const sessionOptions = availableSessions.map((session) => ({ value: sessionKey(session.remoteId || 'local', session.id), label: `${cleanTitle(session.title) || session.id}${session.remoteName ? ` · ${session.remoteName}` : ''}` }));
  const selectedSessionKey = form.sessionId ? sessionKey(form.remoteId, form.sessionId) : '';
  if (selectedSessionKey && !sessionOptions.some((option) => option.value === selectedSessionKey)) sessionOptions.unshift({ value: selectedSessionKey, label: form.sessionId });
  const agentOptions = ['', ...new Set([...catalog.agents, form.agent].filter(Boolean))].map((value) => ({ value, label: value || 'Default agent' }));
  const modelOptions = ['', ...new Set([...catalog.models, form.model].filter(Boolean))].map((value) => ({ value, label: value || 'Default model' }));

  return <form onSubmit={submit}>
      {error && <InlineAlert>{error}</InlineAlert>}
      <fieldset disabled={busy} className={styles.fields}>
        <label className={styles.field}>Name<TextField required data-autofocus value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></label>
        <label className={styles.field}>Prompt<TextareaField className={styles.prompt} required value={form.prompt} onChange={(event) => setForm({ ...form, prompt: event.target.value })} /></label>
        <div className={styles.field}><span>Project</span><SearchSelect value={selectedProjectKey} options={projectOptions} ariaLabel="Project" placeholder="Select a project" searchLabel="Search projects" disabled={busy} onChange={(key) => {
          const project = availableProjects.find((item) => sessionKey(item.remoteId || 'local', item.directory) === key);
          if (project) setForm({ ...form, directory: project.directory, remoteId: project.remoteId || 'local', sessionId: '', agent: '', model: '' });
        }} /></div>
        <label className={styles.field}>Trigger<SelectField aria-label="Trigger" value={trigger.inboxId ? `webhook:${trigger.inboxId}` : form.kind} onChange={(event) => {
          const value = event.target.value;
          if (value.startsWith('webhook:')) { setForm({ ...form, kind: 'none' }); setTrigger({ ...trigger, inboxId: value.slice('webhook:'.length) }); }
          else { setForm({ ...form, kind: value as RoutineScheduleKind }); setTrigger({ ...trigger, inboxId: '' }); }
        }}>
          <option value="none">Manual only</option>
          <optgroup label="Schedule"><option value="timeout">Timeout</option><option value="once">Once</option><option value="cron">Cron</option></optgroup>
          {inboxes.length > 0 && <optgroup label="Webhook">{inboxes.map((inbox) => <option key={inbox.id} value={`webhook:${inbox.id}`}>Webhook: {inbox.name || inbox.id}</option>)}</optgroup>}
        </SelectField>{inboxes.length === 0 && <small className={styles.help}>Create an inbox on the Webhook inboxes tab to trigger this routine from a webhook.</small>}</label>
        {trigger.inboxId && <WebhookTriggerFields trigger={trigger} onChange={setTrigger} disabled={busy} />}
        {form.kind === 'timeout' && <label className={styles.field}>Minutes from now<TextField required min="1" type="number" value={form.timeoutMinutes} onChange={(event) => setForm({ ...form, timeoutMinutes: event.target.value })} /></label>}
        {form.kind === 'once' && <label className={styles.field}>Run at<TextField required type="datetime-local" value={form.at} onChange={(event) => setForm({ ...form, at: event.target.value })} /></label>}
        {form.kind === 'cron' && <><label className={styles.field}>Cron expression<TextField required placeholder="0 9 * * *" value={form.cron} onChange={(event) => setForm({ ...form, cron: event.target.value })} /></label><label className={styles.field}>Timezone<TextField required value={form.timezone} onChange={(event) => setForm({ ...form, timezone: event.target.value })} /></label></>}
        <CheckboxField label="Enabled" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} />
        <details className={styles.group}>
          <summary>Session and model</summary><div className={styles.groupFields}>
            <label className={styles.field}>Session<SelectField aria-label="Session" value={form.sessionMode} onChange={(event) => setForm({ ...form, sessionMode: event.target.value as RoutineSessionMode, sessionId: '' })}><option value="new">New session</option><option value="reuse">Reuse session</option><option value="existing">Existing session</option></SelectField><small className={styles.help}>{form.sessionMode === 'new' ? 'Create a fresh session for every run.' : form.sessionMode === 'reuse' ? 'Create one on the first run, then keep using it.' : 'Continue a session from this project.'}</small></label>
            {form.sessionMode === 'existing' && <div className={styles.field}><span>Existing session</span><SearchSelect value={selectedSessionKey} options={sessionOptions} ariaLabel="Existing session" placeholder={sessionsLoading ? 'Loading sessions...' : 'Select a session'} searchLabel="Search sessions" disabled={busy || sessionsLoading || !form.directory} onChange={(key) => {
              const session = availableSessions.find((item) => sessionKey(item.remoteId || 'local', item.id) === key);
              if (session) setForm({ ...form, sessionId: session.id, remoteId: session.remoteId || 'local' });
            }} /></div>}
            <div className={styles.field}><span>Agent</span><SearchSelect value={form.agent} options={agentOptions} ariaLabel="Agent" placeholder="Default agent" searchLabel="Search agents" disabled={busy || catalogLoading || !form.directory} onChange={(agent) => setForm({ ...form, agent })} /></div>
            <div className={styles.field}><span>Model</span><SearchSelect value={form.model} options={modelOptions} ariaLabel="Model" placeholder="Default model" searchLabel="Search models" disabled={busy || catalogLoading || !form.directory} onChange={(model) => setForm({ ...form, model })} /></div>
          </div>
        </details>
        <details className={styles.group}>
          <summary>After a run</summary><div className={styles.groupFields}>
            <CheckboxField label="Delete after a successful run" checked={form.deleteAfterSuccess} onChange={(event) => setForm({ ...form, deleteAfterSuccess: event.target.checked })} />
            <CheckboxField label="Archive session after a successful run" checked={form.archiveSessionAfterSuccess} onChange={(event) => setForm({ ...form, archiveSessionAfterSuccess: event.target.checked })} />
            <div><CheckboxField label="Notify in the Inbox after a successful run" checked={form.notifyOnSuccess} onChange={(event) => setForm({ ...form, notifyOnSuccess: event.target.checked })} /><p className={styles.help}>Runs that fail or are interrupted always notify.</p></div>
          </div>
        </details>
        <details className={styles.group}>
          <summary>Permissions</summary><div className={styles.groupFields}>
            <p className={styles.help}>Applied only when this routine creates a new session. Existing and previously reused sessions keep their current rules.</p>
            <PermissionRulesEditor rules={rules} onChange={setRules} disabled={busy} />
          </div>
        </details>
        <ModalFooter label="Routine form actions"><Button disabled={busy || !form.directory || (form.sessionMode === 'existing' && !form.sessionId)} aria-busy={busy} type="submit" variant="accent">{editing ? 'Save changes' : 'Create routine'}</Button><Button type="button" disabled={busy} onClick={onClose}>Cancel</Button></ModalFooter>
      </fieldset>
    </form>;
}
