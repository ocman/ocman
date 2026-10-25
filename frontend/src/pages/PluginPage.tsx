import { useEffect, useRef, useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { useProjects } from '../lib/queries';
import { callPluginPage, usePluginPages, type PluginPage as Page, type PageDocument, type PageIssue } from '../lib/pluginPages';
import { pluginActions, type PluginAction, type PluginActionRequest } from '../lib/plugins';
import { Button, ButtonGroup } from '../components/Control';
import { ProjectScopePicker } from '../components/ProjectScopePicker';
import { PluginConfigurations, type Configuration } from '../components/PluginConfigurations';
import { shortPath } from '../lib/format';
import { PluginPageFields } from '../components/PluginPageFields';
import { PluginTreePane } from '../components/PluginTreePane';
import { PluginActionDialog } from '../components/PluginActionDialog';
import { PluginPageContent } from '../components/PluginPageContent';
import './PluginPage.css';

export function PluginPage() {
  const { pluginId, pageId } = useParams();
  const [search] = useSearchParams();
  const ownerId = search.get('ownerId') || 'local';
  const catalog = usePluginPages();
  const page = catalog.pages.find((p) => p.ownerId === ownerId && p.pluginId === pluginId && p.page.id === pageId);
  if (!page) return <p role="status">{catalog.loading ? 'Loading plugin…' : 'Plugin unavailable. Check its owner, enablement and page grant in Settings.'}</p>;
  return <Workspace key={`${ownerId}/${pluginId}/${pageId}`} page={page} />;
}

function Workspace({ page }: { page: Page }) {
  const [search, setSearch] = useSearchParams();
  const projects = useProjects();
  const [directory, setDirectory] = useState('');
  const [creating, setCreating] = useState(false);
  const [configuration, setConfiguration] = useState<Configuration | null>(null);
  const choices = (projects.data ?? []).filter((p) => (p.remoteId || 'local') === page.ownerId);
  const back = () => { setDirectory(''); setConfiguration(null); setCreating(false); };
  const projectId = search.get('projectId');
  const itemId = search.get('itemId') || '';
  if (projectId) return <section className="plugin-page"><h1>{page.page.label}</h1>
    <PageEditor key={`${projectId}/${itemId}`} page={page} projectId={projectId} itemId={itemId} onBack={() => {
      const next = new URLSearchParams(search);
      if (itemId) next.delete('itemId'); else next.delete('projectId');
      setSearch(next);
    }} />
  </section>;
  return <section className="plugin-page">
    <h1>{page.page.label}</h1>
    {!directory && !configuration ? <>
      <div className="plugin-page-heading"><div><h2>{page.page.scope === 'plugin' ? 'Workspaces' : 'Configurations'}</h2><p>{page.page.scope === 'plugin' ? 'Open a workspace or create one to get started.' : 'Open a project workspace or create a configuration.'}</p></div>
        {page.page.scope !== 'plugin' && <Button variant="accent" onClick={() => setCreating(true)}>New configuration</Button>}</div>
      {creating && <div className="plugin-page-create"><p>Choose the project that owns this workspace.</p>
        <ProjectScopePicker projects={choices} value="" onlyProjects label="Configuration project" placeholder="Choose a project…"
          onChange={(dir) => { if (choices.some((p) => p.directory === dir)) setDirectory(dir); }} />
        <Button onClick={() => setCreating(false)}>Cancel</Button></div>}
      {page.page.scope === 'plugin'
        ? <PageEditor key={itemId} page={page} projectId="" itemId={itemId} onBack={() => {
          const next = new URLSearchParams(search); next.delete('itemId'); setSearch(next);
        }} />
        : <PluginConfigurations page={page} directories={[...new Set(choices.map((p) => p.directory))]} onOpen={setConfiguration} />}
    </> : <>
      <div><h2>{shortPath(configuration?.directory ?? directory)}</h2><p>{page.ownerId} · {configuration?.directory ?? directory}</p></div>
      {configuration ? <PageEditor page={page} projectId={configuration.projectId} initialDocument={configuration.document} onBack={back} />
        : <ProjectPage key={directory} page={page} directory={directory} onBack={back} />}
    </>}
    {projects.isError && <p role="alert">Could not load projects.</p>}
  </section>;
}

function ProjectPage({ page, directory, onBack }: { page: Page; directory: string; onBack: () => void }) {
  const project = useQuery({ queryKey: ['plugin-page-project', page.ownerId, directory], retry: false,
    queryFn: async ({ signal }) => (await api.sessions({ dir: directory, limit: 0 }, signal))
      .find((s) => s.directory === directory && (s.remoteId || 'local') === page.ownerId)?.projectId ?? null });
  if (project.isPending) return <><Button onClick={onBack}>Back to configurations</Button><p role="status">Loading project…</p></>;
  if (!project.data || project.isError) return <><Button onClick={onBack}>Back to configurations</Button><p role="alert">No owner-local project identity is available for this project.</p></>;
  return <PageEditor key={project.data} page={page} projectId={project.data} onBack={onBack} />;
}

function pageContext(itemId: string, cursor = '') {
  return { ...(itemId ? { itemId } : {}), ...(cursor ? { cursor } : {}) };
}

function PageEditor({ page, projectId, initialDocument, onBack, itemId = '' }: { page: Page; projectId: string; initialDocument?: PageDocument; onBack: () => void; itemId?: string }) {
  const client = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [creatingWorkspace, setCreatingWorkspace] = useState(false);
  const workspaceHome = page.page.scope === 'plugin' && !projectId && !itemId;
  const [cursor, setCursor] = useState('');
  const [issues, setIssues] = useState<PageIssue[]>([]);
  const saveController = useRef<AbortController | null>(null);
  useEffect(() => () => saveController.current?.abort(), []);
  const context = pageContext(itemId, cursor);
  const rootQueryKey = ['plugin-page-document', page.ownerId, page.pluginId, page.page.id, projectId];
  const queryKey = [...rootQueryKey, ...(itemId || cursor ? [itemId, cursor] : [])];
  const document = useQuery({ queryKey, initialData: !cursor && !itemId ? initialDocument : undefined,
    queryFn: ({ signal }) => itemId || cursor ? callPluginPage(page, projectId, 'read', undefined, signal, context) : callPluginPage(page, projectId, 'read', undefined, signal), retry: false, gcTime: 0,
    enabled: !busy, staleTime: 10_000, refetchInterval: busy ? false : 10_000 });
  const [draft, setDraft] = useState<PageDocument | null>(null);
  const [message, setMessage] = useState('');
  const [action, setAction] = useState<{ label: string; request: PluginActionRequest } | null>(null);
  const workspaceId = page.page.scope === 'plugin' && !projectId ? itemId : '';
  const placement = workspaceId ? 'plugin' : 'project';
  const actionContext = { ownerId: page.ownerId, ...(workspaceId ? { workspaceId } : { projectId }) };
  const actions = useQuery({ queryKey: ['plugin-page-actions', page.ownerId, page.pluginId, projectId, workspaceId], retry: false, enabled: !!projectId || !!workspaceId,
    queryFn: ({ signal }) => pluginActions.list(placement, actionContext, signal) });
  const current = draft ?? document.data;
  async function save() {
    if (!current) return;
    setBusy(true); setMessage(''); setIssues([]);
    const controller = new AbortController();
    saveController.current = controller;
    try {
      const saved = await callPluginPage(page, projectId, 'save', current, controller.signal, pageContext(itemId));
      if (controller.signal.aborted) return;
      if (saved.issues?.length) { setIssues(saved.issues); setMessage('Fix the validation errors before saving.'); return; }
      await client.cancelQueries({ queryKey: rootQueryKey });
      client.setQueryData([...rootQueryKey, ...(itemId ? [itemId, ''] : [])], saved);
      setCursor('');
      setDraft(null);
      setCreatingWorkspace(false);
      setMessage(workspaceHome ? 'Workspace created.' : 'Saved.');
    } catch { if (!controller.signal.aborted) setMessage('Save failed. Check field values. Reload before retrying if another editor changed this project.'); }
    finally { setBusy(false); }
  }
  function run(selected: PluginAction) {
    setAction({ label: selected.action.label, request: { pluginId: page.pluginId, ownerId: page.ownerId, actionId: selected.action.id,
      operationId: crypto.randomUUID(), placement, surface: 'command-palette', context: actionContext } });
  }
  if (!current) return <>{!workspaceHome && <Button onClick={onBack}>{itemId ? 'Back to list' : 'Back to configurations'}</Button>}
    <p role={document.isError ? 'alert' : 'status'}>{document.isError ? workspaceHome ? 'Could not load workspaces. Check that the plugin is enabled and its grants are approved.' : 'Could not read this workspace or item. Check that it still exists and the owner is available.' : 'Loading workspace…'}</p>
    <Button onClick={() => void document.refetch()}>Retry</Button>
    {workspaceHome && document.isError && <Link to="/settings?tab=plugins">Open plugin settings</Link>}</>;
  return <>
    {(projectId || itemId) && <Button disabled={busy} onClick={() => { if (!draft || window.confirm('Discard unsaved edits?')) onBack(); }}>{itemId ? 'Back to list' : 'Back to configurations'}</Button>}
    {workspaceHome && !!current.fields?.length && !creatingWorkspace && <Button variant="accent" onClick={() => setCreatingWorkspace(true)}>New workspace</Button>}
    <PluginPageContent page={page} projectId={projectId} document={current} disabled={busy || !!draft} cursor={cursor} onCursor={setCursor} emptyText={workspaceHome ? 'No workspaces yet. Choose New workspace to get started.' : undefined} />
    {!!current.fields?.length && (!workspaceHome || creatingWorkspace) && <fieldset disabled={busy} className="plugin-page-editor">
      <PluginPageFields fields={current.fields} values={current.values} ownerId={page.ownerId} issues={issues} onChange={(values) => { setMessage(''); setIssues([]); setDraft({ ...current, values }); }} />
      <ButtonGroup label="Configuration controls"><Button variant="accent" onClick={() => void save()}>{workspaceHome ? 'Create workspace' : 'Save configuration'}</Button>
        {workspaceHome ? <Button onClick={() => { setDraft(null); setIssues([]); setCreatingWorkspace(false); }}>Cancel</Button>
          : <Button onClick={() => { if (draft && !window.confirm('Discard unsaved edits and reload?')) return; setDraft(null); setIssues([]); setMessage(''); void document.refetch(); }}>Reload configuration</Button>}</ButtonGroup>
    </fieldset>}
    {message && <p role="status">{message}</p>}
    {issues.length > 0 && <ul aria-label="Validation errors">{issues.map((issue, i) => <li key={i}>{issue.path || 'Form'}: {issue.message}</li>)}</ul>}
    {(projectId || workspaceId) && <><h2>Actions</h2>
    <p>Save changes before running an action. Required confirmations still apply.</p>
    <ButtonGroup label="Plugin actions">{actions.data?.filter((a) => a.pluginId === page.pluginId && a.action.id !== 'project-config').map((a) =>
      <Button key={a.action.id} disabled={busy || !!draft} onClick={() => run(a)}>{a.action.label}</Button>)}</ButtonGroup>
    {actions.isError && <p role="alert">Could not load plugin actions.</p>}</>}
    {!workspaceHome && <><h2>Results</h2>
    <PluginTreePane status={document.data?.tree ?? current.tree} label="Results" loading={document.isFetching} error={document.error} refresh={() => document.refetch()} /></>}
    {action && <PluginActionDialog label={action.label} request={action.request} onClose={() => { setAction(null); void document.refetch(); }} />}
  </>;
}
