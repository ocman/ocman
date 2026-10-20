import { useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { useProjects } from '../lib/queries';
import { callPluginPage, usePluginPages, type PluginPage as Page, type PageDocument } from '../lib/pluginPages';
import { pluginActions, type PluginAction, type PluginActionRequest } from '../lib/plugins';
import { Button, ButtonGroup } from '../components/Control';
import { ProjectScopePicker } from '../components/ProjectScopePicker';
import { PluginConfigurations, type Configuration } from '../components/PluginConfigurations';
import { shortPath } from '../lib/format';
import { PluginPageFields } from '../components/PluginPageFields';
import { PluginTreePane } from '../components/PluginTreePane';
import { PluginActionDialog } from '../components/PluginActionDialog';
import './PluginPage.css';

export function PluginPage() {
  const { pluginId, pageId } = useParams();
  const [search] = useSearchParams();
  const ownerId = search.get('ownerId') || 'local';
  const catalog = usePluginPages();
  const page = catalog.pages.find((p) => p.ownerId === ownerId && p.pluginId === pluginId && p.page.id === pageId);
  if (!page) return <p role="status">{catalog.loading ? 'Loading plugin…' : 'Plugin unavailable. Check its owner, enablement and page.project grant in Settings.'}</p>;
  return <Workspace key={`${ownerId}/${pluginId}/${pageId}`} page={page} />;
}

function Workspace({ page }: { page: Page }) {
  const projects = useProjects();
  const [directory, setDirectory] = useState('');
  const [creating, setCreating] = useState(false);
  const [configuration, setConfiguration] = useState<Configuration | null>(null);
  const choices = (projects.data ?? []).filter((p) => (p.remoteId || 'local') === page.ownerId);
  const back = () => { setDirectory(''); setConfiguration(null); setCreating(false); };
  return <section className="plugin-page">
    <h1>{page.page.label}</h1>
    {!directory && !configuration ? <>
      <div className="plugin-page-heading"><div><h2>Configurations</h2><p>Each configuration groups the targets, code projects and scan settings for one project.</p></div>
        <Button variant="accent" onClick={() => setCreating(true)}>New configuration</Button></div>
      {creating && <div className="plugin-page-create"><p>Choose the project that owns this configuration. You can attach more code projects to each target.</p>
        <ProjectScopePicker projects={choices} value="" onlyProjects label="Configuration project" placeholder="Choose a project…"
          onChange={(dir) => { if (choices.some((p) => p.directory === dir)) setDirectory(dir); }} />
        <Button onClick={() => setCreating(false)}>Cancel</Button></div>}
      <PluginConfigurations page={page} directories={[...new Set(choices.map((p) => p.directory))]} onOpen={setConfiguration} />
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

function PageEditor({ page, projectId, initialDocument, onBack }: { page: Page; projectId: string; initialDocument?: PageDocument; onBack: () => void }) {
  const client = useQueryClient();
  const [busy, setBusy] = useState(false);
  const queryKey = ['plugin-page-document', page.ownerId, page.pluginId, page.page.id, projectId];
  const document = useQuery({ queryKey, initialData: initialDocument,
    queryFn: ({ signal }) => callPluginPage(page, projectId, 'read', undefined, signal), retry: false, gcTime: 0,
    enabled: !busy, staleTime: 10_000, refetchInterval: busy ? false : 10_000 });
  const [draft, setDraft] = useState<PageDocument | null>(null);
  const [message, setMessage] = useState('');
  const [action, setAction] = useState<{ label: string; request: PluginActionRequest } | null>(null);
  const actions = useQuery({ queryKey: ['plugin-page-actions', page.ownerId, page.pluginId, projectId], retry: false,
    queryFn: ({ signal }) => pluginActions.list('project', { ownerId: page.ownerId, projectId }, signal) });
  const current = draft ?? document.data;
  async function save() {
    if (!current) return;
    setBusy(true); setMessage('');
    try {
      const saved = await callPluginPage(page, projectId, 'save', current);
      await client.cancelQueries({ queryKey, exact: true });
      client.setQueryData(queryKey, saved);
      setDraft(null);
      setMessage('Saved.');
    } catch { setMessage('Save failed. Check field values. Reload before retrying if another editor changed this project.'); }
    finally { setBusy(false); }
  }
  function run(selected: PluginAction) {
    setAction({ label: selected.action.label, request: { pluginId: page.pluginId, ownerId: page.ownerId, actionId: selected.action.id,
      operationId: crypto.randomUUID(), placement: 'project', surface: 'command-palette', context: { ownerId: page.ownerId, projectId } } });
  }
  if (!current) return <><p role={document.isError ? 'alert' : 'status'}>{document.isError ? 'Could not read target configuration. Check the plugin and file permissions.' : 'Loading target configuration…'}</p>
    <Button onClick={() => void document.refetch()}>Retry</Button></>;
  return <>
    <Button disabled={busy} onClick={() => { if (!draft || window.confirm('Discard unsaved edits?')) onBack(); }}>Back to configurations</Button>
    <fieldset disabled={busy} className="plugin-page-editor">
      <PluginPageFields fields={current.fields} values={current.values} ownerId={page.ownerId} onChange={(values) => { setMessage(''); setDraft({ ...current, values }); }} />
      <ButtonGroup label="Configuration controls"><Button variant="accent" onClick={() => void save()}>Save configuration</Button>
        <Button onClick={() => { if (draft && !window.confirm('Discard unsaved edits and reload?')) return; setDraft(null); setMessage(''); void document.refetch(); }}>Reload configuration</Button></ButtonGroup>
    </fieldset>
    {message && <p role="status">{message}</p>}
    <h2>Scan controls</h2>
    <p>Save target changes before running a scan. Scope disclosure and engagement consent are still required.</p>
    <ButtonGroup label="Scan controls">{actions.data?.filter((a) => a.pluginId === page.pluginId && a.action.id !== 'project-config').map((a) =>
      <Button key={a.action.id} disabled={busy || !!draft} onClick={() => run(a)}>{a.action.label}</Button>)}</ButtonGroup>
    {actions.isError && <p role="alert">Could not load scan controls.</p>}
    <h2>Results for the saved selected target</h2>
    <PluginTreePane status={document.data?.tree ?? current.tree} label="Findings" loading={document.isFetching} error={document.error} refresh={() => document.refetch()} />
    {action && <PluginActionDialog label={action.label} request={action.request} onClose={() => { setAction(null); void document.refetch(); }} />}
  </>;
}
