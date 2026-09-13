import { useDeferredValue, useId, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { Link, NavLink, useNavigate, useParams } from 'react-router-dom';
import { MarkdownContent } from '../components/assistant/MarkdownText';
import { EpicGraph } from './EpicGraph';
import { proposalIssues } from './factoryGraph';
import { Button, SearchField, SelectField } from '../components/Control';
import { SearchSelect } from '../components/SearchSelect';
import { ProjectLabel } from '../components/ProjectLabel';
import { StatusBadge } from '../components/StatusBadge';
import { DataTableGroup, DataTableRow } from '../components/DataTable';
import { FactoryStartedToast } from '../components/FactoryStartedToast';
import { FactoryImplementationModel } from '../components/FactoryImplementationModel';
import { useFactoryImplementationModel } from '../components/useFactoryImplementationModel';
import { useClaimFactoryPlan, useCloseFactoryEpic, useCloseFactoryMol, useCreateWorkEpic, useDecideFactoryPlanGate, useFactoryCapacityPolicy, useFactoryFormula, useFactoryFormulas, useFactoryGraphIssues, useFactoryIssues, useFactoryProposals, useFactoryQueue, useFactoryRemovedIssues, useInvestigateFactoryUnblock, useMaterializeFactoryPlan, useMutateFactoryGraph, usePourFactoryEpic, usePreviewFactoryFormula, useProjects, useReopenFactoryIssue, useResolveFactoryAuthorityGate, useResolveFactoryProjectGate, useResolveFactoryRecoveryGate, useSaveFactoryFormula, useSessions, useSetFactoryCapacityPolicy, useSetFactoryEpicPaused, useValidateFactoryFormula, useWorkEpic, useWorkEpics } from '../lib/queries';
import type { FactoryAttempt, FactoryEpic, FactoryFormula, FactoryGraphMutation, FactoryIssue, FactoryQueueItem, Session } from '../lib/api';

const TRACER_FORMULA_ID = 'ocman/tracer';
import { Modal } from '../components/Modal';
import { EpicCell, FactoryIssueRow, IssueDrawer, ProjectCell, type EpicRef } from './FactoryIssues';
import './Factory.css';

function newInstantiationID() {
  return crypto.randomUUID?.() ?? Array.from(crypto.getRandomValues(new Uint32Array(4)), (value) => value.toString(16).padStart(8, '0')).join('');
}

function QueryError({ error, retry }: { error: unknown; retry: () => void }) {
  return <div className="oc-error-banner" role="alert">
    {error instanceof Error ? error.message : 'Factory data is unavailable.'}
    <Button type="button" onClick={retry}>Retry</Button>
  </div>;
}

const isClosed = (status: string) => status === 'closed' || status === 'completed';
const statusLabel = (status: string) => status.replaceAll('_', ' ').replace(/^./, (letter) => letter.toUpperCase());

function IssueList({ epicID }: { epicID: string }) {
  const issues = useFactoryIssues(epicID);
  const [selected, setSelected] = useState<FactoryIssue>();
	const [query, setQuery] = useState('');
	const [statusFilter, setStatusFilter] = useState('active');
	const [kind, setKind] = useState('');
	const search = useDeferredValue(query.trim().toLowerCase());
  if (issues.isLoading) return <p role="status">Loading issues…</p>;
  if (issues.isError) return <QueryError error={issues.error} retry={() => void issues.refetch()} />;
	// ponytail: Mols are containers, not work, so keep them out of the ticket inventory.
	const inventory = issues.data?.filter((issue) => issue.kind !== 'mol') ?? [];
	const kinds = [...new Set(inventory.map((issue) => issue.kind))].sort();
	const filtered = inventory.filter((issue) => `${issue.id} ${issue.title} ${issue.kind} ${issue.status}`.toLowerCase().includes(search) && (!kind || issue.kind === kind));
	const closedCount = filtered.filter((issue) => isClosed(issue.status)).length;
	const visible = filtered.filter((issue) => statusFilter === 'all' || (statusFilter === 'closed' ? isClosed(issue.status) : !isClosed(issue.status)));
	const statusGroups: Record<string, string> = { in_progress: 'In progress', blocked: 'Blocked', retry_wait: 'Waiting', deferred: 'Waiting', open: 'Open', closed: 'Closed', completed: 'Closed' };
	const groupFor = (issue: FactoryIssue) => statusGroups[issue.status] ?? statusLabel(issue.status);
	const order = ['In progress', 'Blocked', 'Open', 'Waiting', 'Closed'];
	const groups = [...new Set(visible.map(groupFor))].sort((a, b) => order.indexOf(a) - order.indexOf(b));
	return <><InventoryToolbar label="Find board issues" value={query} onChange={setQuery}><label>Board status<SelectField value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}><option value="active">Open only</option><option value="all">Open + closed</option><option value="closed">Closed only</option></SelectField></label><label>Board type<SelectField value={kind} onChange={(event) => setKind(event.target.value)}><option value="">All types</option>{kinds.map((value) => <option key={value} value={value}>{value}</option>)}</SelectField></label><span className="factory-result-count" aria-live="polite">{visible.length} shown{statusFilter === 'active' && closedCount > 0 ? ` · ${closedCount} closed hidden` : statusFilter === 'all' && closedCount > 0 ? ` · ${closedCount} closed` : ''}</span></InventoryToolbar>{!visible.length ? <p className="oc-empty">{inventory.length ? 'No issues match these filters.' : 'This epic has no issues yet.'}</p> : <div className="factory-list" aria-label="Epic issues by status">{groups.map((group) => { const items = visible.filter((issue) => groupFor(issue) === group); return <DataTableGroup key={group} label={group} noun="issues" count={items.length} markerClassName={`factory-status-dot--${group.toLowerCase().replaceAll(' ', '-')}`}>{items.map((issue) => <FactoryIssueRow key={issue.id} issue={issue} onOpen={() => setSelected(issue)} />)}</DataTableGroup>; })}</div>}{selected && <IssueDrawer key={selected.id} issue={selected} onClose={() => setSelected(undefined)} />}</>;
}

type DispatchEvidence = Pick<FactoryIssue, 'dispatchState' | 'blockers' | 'retryAt' | 'retryAttempts' | 'outcomeReason'>;

function BlockerEvidence({ blockers }: { blockers?: FactoryIssue['blockers'] }) {
  if (!blockers?.length) return null;
  return <>{blockers.map(({ id, epicId, outcome, reason }, index) => <span key={id}>{index > 0 && '; '}<Link to={`/factory/issues/${encodeURIComponent(id)}`} aria-label={`Open blocker ${id}`}>{id}{epicId ? ` in Work Epic ${epicId}` : ''}</Link> {outcome || 'pending'}{reason ? `: ${reason}` : ''}</span>)}</>;
}

function DispatchExplanation({ item }: { item: DispatchEvidence }) {
  const blocker = <BlockerEvidence blockers={item.blockers} />;
  const hasBlocker = Boolean(item.blockers?.length);
  switch (item.dispatchState) {
    case 'terminally_blocked': return <span>Dispatch: cannot proceed because {hasBlocker ? blocker : 'a prerequisite failed'}.</span>;
    case 'not_applicable': return <span>Dispatch: {item.outcomeReason || <>not applicable because the recovery condition was not met{hasBlocker && <> ({blocker})</>}.</>}</span>;
    case 'deferred': return <span>Dispatch: delayed{item.outcomeReason ? `: ${item.outcomeReason}` : ''}.</span>;
    case 'retry_wait': return <span>Dispatch: retry {item.retryAttempts ?? 0} scheduled for {item.retryAt ? new Date(item.retryAt).toISOString() : 'a later time'}.</span>;
    case 'waiting': return <span>Dispatch: waiting for prerequisites{hasBlocker && <>: {blocker}</>}.</span>;
    case 'ready': return <span>Dispatch: ready.</span>;
    case 'running': return <span>Dispatch: in progress.</span>;
    case 'completed': return <span>Dispatch: complete.</span>;
    case 'reference': return <span>Dispatch: reference work is not scheduled.</span>;
		case 'paused': return <span>Dispatch: epic paused.</span>;
    default: return null;
  }
}

function FactoryDataRow({ id, idLabel = 'Issue', title, epic, detail, actions }: { id: string; idLabel?: string; title: ReactNode; epic?: EpicRef; detail?: ReactNode; actions?: ReactNode }) {
	return <DataTableRow className="factory-grid-row" primary={title} secondary={<><span className="oc-data-table-field-label">{idLabel} </span>{epic ? <Link to={`/factory/epics/${encodeURIComponent(epic.id)}`}>{id}</Link> : id}</>} meta={<><ProjectCell path={epic?.initialProject} /><EpicCell id={epic?.id} goal={epic?.goal} /><div className="factory-row-detail">{detail}</div><div className="factory-cell factory-cell--actions">{actions}</div></>} />;
}


function Drawer({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  return <Modal label={title} onClose={onClose} backdropClassName="factory-issue-backdrop" dialogClassName="factory-issue-drawer">
    <header className="factory-form-drawer-header"><h2>{title}</h2><button className="factory-issue-close" type="button" onClick={onClose} aria-label={`Close ${title}`} title="Close"><i className="bi bi-x-lg" aria-hidden="true" /></button></header>
    {children}
  </Modal>;
}

function GraphControls({ epicID, issues, allIssues }: { epicID: string; issues: FactoryIssue[]; allIssues: FactoryIssue[] }) {
  const mutate = useMutateFactoryGraph(epicID);
  const [action, setAction] = useState<FactoryGraphMutation['action']>('create');
  const [confirmed, setConfirmed] = useState(false);
  const [issueID, setIssueID] = useState('');
  const [mutationStatus, setMutationStatus] = useState('');
  const openIssues = issues.filter((issue) => issue.status === 'open');
  const targets = allIssues.filter((issue) => issue.status === 'open');
  const selectedIssueID = issueID || openIssues[0]?.id || '';
  const selectedIssue = openIssues.find((issue) => issue.id === selectedIssueID);
  const noTarget = (action === 'reparent' && !openIssues.some((issue) => issue.id !== selectedIssueID)) || ((action === 'link' || action === 'unlink') && !targets.some((issue) => issue.id !== selectedIssueID));
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const mutation: FactoryGraphMutation = { action, issueId: selectedIssueID };
    if (action === 'create') Object.assign(mutation, { parentId: selectedIssueID, kind: String(form.get('kind')), title: String(form.get('title')).trim(), description: String(form.get('description')).trim(), requirement: String(form.get('requirement')) });
    if (action === 'edit') Object.assign(mutation, { title: String(form.get('title')).trim(), description: String(form.get('description')).trim() });
    if (action === 'reparent') Object.assign(mutation, { parentId: String(form.get('parentId')), requirement: String(form.get('requirement')) });
    if (action === 'link' || action === 'unlink') Object.assign(mutation, { dependsOnId: String(form.get('dependsOnId')), dependencyType: String(form.get('dependencyType')) as 'blocks' | 'on_failure' });
    try { await mutate.mutateAsync(mutation); setConfirmed(false); setMutationStatus(action === 'delete' ? 'Work soft-deleted. It remains in Factory audit history.' : 'Graph updated.'); } catch { /* The mutation error is rendered below. */ }
  }
  return <section className="factory-graph-controls" aria-label="Manage graph">
    <p>Only open, unstarted work can change. Dependency targets name their Work Epic.</p>
    {!openIssues.length ? <p className="oc-empty">No eligible work is available.</p> : <form onSubmit={(event) => void submit(event)}>
      <label>Action<select aria-label="Graph action" value={action} onChange={(event) => { setAction(event.target.value as FactoryGraphMutation['action']); setConfirmed(false); }}><option value="create">Create child work</option><option value="edit">Edit work</option><option value="reparent">Reparent work</option><option value="link">Link dependency</option><option value="unlink">Unlink dependency</option><option value="delete">Soft-delete work</option></select></label>
      <label>{action === 'create' ? 'Parent work' : 'Work'}<select aria-label={action === 'create' ? 'Parent work' : 'Work'} name="issueId" value={selectedIssueID} onChange={(event) => setIssueID(event.target.value)}>{openIssues.map((issue) => <option key={issue.id} value={issue.id}>{issue.title} ({issue.id})</option>)}</select></label>
      {action === 'create' && <><label>Kind<select name="kind"><option value="task">Task</option><option value="implementation">Implementation</option></select></label><label>Title<input aria-label="Work title" name="title" required /></label><label>Description<textarea aria-label="Work description" name="description" /></label></>}
      {action === 'edit' && <><label>Title<input key={`title-${selectedIssueID}`} aria-label="Work title" name="title" required defaultValue={selectedIssue?.title} /></label><label>Description<textarea key={`description-${selectedIssueID}`} aria-label="Work description" name="description" defaultValue={selectedIssue?.description} /></label></>}
      {(action === 'create' || action === 'reparent') && <label>Requirement<select name="requirement"><option value="required">Required</option><option value="optional">Optional</option></select></label>}
      {action === 'reparent' && <label>New parent<select aria-label="New parent" name="parentId">{openIssues.filter((issue) => issue.id !== selectedIssueID).map((issue) => <option key={issue.id} value={issue.id}>{issue.title} ({issue.id})</option>)}</select></label>}
      {(action === 'link' || action === 'unlink') && <><label>Dependency target<select aria-label="Dependency target" name="dependsOnId">{targets.filter((issue) => issue.id !== selectedIssueID).map((issue) => <option key={issue.id} value={issue.id}>{issue.epicId === epicID ? 'This Work Epic' : `Work Epic ${issue.epicId}`}: {issue.title} ({issue.id})</option>)}</select></label><label>Dependency type<select name="dependencyType"><option value="blocks">Blocks</option><option value="on_failure">On failure</option></select></label></>}
      {action === 'delete' && <label><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} /> I understand this soft-deletes this work and its descendants.</label>}
      {noTarget && <p role="alert">Select another open work item for this change.</p>}
      <Button type="submit" variant="accent" disabled={mutate.isPending || noTarget || (action === 'delete' && !confirmed)}>{mutate.isPending ? 'Saving…' : action === 'delete' ? 'Soft-delete work' : 'Save graph change'}</Button>
    </form>}
    {mutate.isError && <p role="alert">{mutate.error instanceof Error ? mutate.error.message : 'Could not change the graph.'}</p>}
    {mutationStatus && <p role="status">{mutationStatus}</p>}
  </section>;
}

function joinFormulaItems(items: string[]) {
  if (items.length < 2) return items[0] ?? '';
  if (items.length === 2) return `${items[0]} and ${items[1]}`;
  return `${items.slice(0, -1).join(', ')}, and ${items.at(-1)}`;
}

function describeFormula(formula?: FactoryFormula) {
  if (!formula || formula.id === TRACER_FORMULA_ID) return 'Creates a plan, waits for approval, then materializes the approved plan.';
  const work = joinFormulaItems(formula.nodes.map(({ key, kind }) => `${key} (${kind})`));
  const dependencies = formula.edges.filter((edge) => edge.type === 'blocks').map((edge) => `${edge.from} blocks ${edge.to}`);
  return `Creates ${work || 'no work'}${dependencies.length ? `; ${joinFormulaItems(dependencies)}.` : '.'}`;
}

function CreateEpic({ onCreated }: { onCreated?: () => void }) {
  const create = useCreateWorkEpic();
  const formulas = useFactoryFormulas();
  const projects = useProjects();
  const [error, setError] = useState('');
  const [initialProject, setInitialProject] = useState('');
	const [secondaryProjects, setSecondaryProjects] = useState<string[]>([]);
	const [acknowledgedProjects, setAcknowledgedProjects] = useState<string[]>([]);
  const [formula, setFormula] = useState('');
  const pendingInstantiation = useRef<{ key: string; id: string } | undefined>(undefined);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const goal = String(form.get('goal')).trim();
    const brief = String(form.get('brief')).trim();
    const selectedFormula = String(form.get('formula'));
		const acknowledgeLocalExecution = form.get('acknowledgeLocalExecution') === 'on';
    const [formulaId, formulaRevision] = selectedFormula.split('@');
    if (!initialProject) {
      setError('Select an initial Factory project.');
      return;
    }
		if (!acknowledgeLocalExecution) {
			setError('Acknowledge local command execution.');
			return;
		}
		const missingAcknowledgement = secondaryProjects.find((path) => !acknowledgedProjects.includes(path));
		if (missingAcknowledgement) {
			setError(`Acknowledge local command execution in ${missingAcknowledgement}.`);
			return;
		}
		const key = JSON.stringify([goal, brief, initialProject, secondaryProjects, selectedFormula]);
    try {
      if (pendingInstantiation.current?.key !== key) {
        pendingInstantiation.current = { key, id: newInstantiationID() };
      }
      await create.mutateAsync({
        instantiationId: pendingInstantiation.current.id,
        goal,
        brief: brief || undefined,
        initialProject,
				acknowledgeLocalExecution,
				...(secondaryProjects.length && { projects: secondaryProjects.map((path) => ({ path, acknowledgeLocalExecution: true })) }),
        ...(formula && { formulaId, formulaRevision: Number(formulaRevision) }),
      });
      pendingInstantiation.current = undefined;
      formElement.reset();
      setInitialProject('');
			setSecondaryProjects([]);
			setAcknowledgedProjects([]);
      setFormula('');
      setError('');
      onCreated?.();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not create epic.');
    }
  }
  const selectedFormula = formulas.data?.find((item) => `${item.id}@${item.version}` === formula);
  return <form className="factory-create" onSubmit={(event) => void submit(event)}>
    <div className="factory-field"><label>Goal<input name="goal" required maxLength={80} aria-describedby="factory-goal-help" /></label><p id="factory-goal-help">A short clear title for the outcome this Factory work should deliver.</p></div>
    <div className="factory-field"><label>Brief<textarea name="brief" aria-describedby="factory-brief-help" /></label><p id="factory-brief-help">Optional context, constraints, and decisions for the planning work.</p></div>
		<div className="factory-field"><label>Initial Factory project<SearchSelect value={initialProject} ariaLabel="Initial Factory project" placeholder={projects.isLoading ? 'Loading projects…' : 'Select a project'} searchLabel="Search projects" disabled={projects.isLoading || !projects.data?.some((project) => !project.archived)} onChange={(value) => { setInitialProject(value); setSecondaryProjects((current) => current.filter((path) => path !== value)); setAcknowledgedProjects((current) => current.filter((path) => path !== value)); setError(''); }} options={projects.data?.filter((project) => !project.archived && !project.remoteId).map((project) => ({ value: project.directory, label: project.directory, displayLabel: <ProjectLabel path={project.directory} /> })) ?? []} /></label><p>The local project where Factory starts work. Commands run on this machine.</p></div>
		<div className="factory-field"><label>Additional projects<select multiple aria-label="Additional projects" value={secondaryProjects} onChange={(event) => { const selected = [...event.currentTarget.selectedOptions].map(({ value }) => value); setSecondaryProjects(selected); setAcknowledgedProjects((current) => current.filter((path) => selected.includes(path))); setError(''); }}>{projects.data?.filter((project) => !project.archived && !project.remoteId && project.directory !== initialProject).map((project) => <option key={project.directory} value={project.directory} aria-label={`Additional project ${project.directory}`}>{project.directory}</option>)}</select></label><p>Optional local repositories this Epic may use.</p></div>
		{secondaryProjects.map((path) => <label key={path}><input type="checkbox" checked={acknowledgedProjects.includes(path)} onChange={(event) => setAcknowledgedProjects((current) => event.target.checked ? [...current, path] : current.filter((item) => item !== path))} />Allow Factory agents to run commands in {path}</label>)}
    {projects.isError && <p role="alert">Could not load Factory projects.</p>}
    <div className="factory-field"><label>Formula<SelectField name="formula" value={formula} onChange={(event) => setFormula(event.target.value)} aria-describedby="factory-formula-help"><option value="">Built-in tracer</option>{formulas.data?.filter((item) => item.id !== TRACER_FORMULA_ID).map((item) => <option key={`${item.id}@${item.version}`} value={`${item.id}@${item.version}`}>{item.name} · {item.id}@{item.version}</option>)}</SelectField></label><p id="factory-formula-help">Defines the initial work graph. Formula revisions are immutable.</p><p aria-live="polite">{describeFormula(selectedFormula)}</p></div>
		<label><input type="checkbox" name="acknowledgeLocalExecution" />Allow Factory agents to run commands in this project</label>
    <Button type="submit" variant="accent" disabled={create.isPending}>{create.isPending ? 'Creating…' : 'Create epic'}</Button>
    {error && <p role="alert">{error}</p>}
  </form>;
}

function RecoveryGateItem({ issue, epic }: { issue: FactoryIssue; epic?: EpicRef }) {
	const resolve = useResolveFactoryRecoveryGate();
	const gate = issue.recovery!;
	const pending = gate.resolution === 'resume_pending';
	const [response, setResponse] = useState(gate.response ?? gate.choices?.[0] ?? '');
	const act = (action: 'resume' | 'retry' | 'cancel') => resolve.mutate({ id: gate.issueId, action, response: action === 'resume' ? response : '' });
	// Only the clicked button reports the in-flight request; the others share the mutation.
	const busy = (action: string) => resolve.isPending && resolve.variables?.action === action;
	return <FactoryDataRow id={issue.id} epic={epic} title={<strong>{issue.title}</strong>} detail={<><strong>{gate.question || issue.title}</strong>{gate.reason && <span>{gate.reason}</span>}</>} actions={<div className="factory-inbox-control">{gate.choices?.length ? <label>Response<select aria-label={`Recovery response for ${issue.id}`} value={response} disabled={pending} onChange={(event) => setResponse(event.target.value)}>{gate.choices.map((choice) => <option key={choice} value={choice}>{choice}</option>)}</select></label> : <label>Response<input aria-label={`Recovery response for ${issue.id}`} value={response} disabled={pending} onChange={(event) => setResponse(event.target.value)} /></label>}<div className="factory-inbox-actions">{pending ? <Button type="button" variant="accent" disabled={resolve.isPending} onClick={() => act('resume')}>{busy('resume') ? 'Resuming…' : 'Retry resume'}</Button> : <><Button type="button" variant="accent" disabled={resolve.isPending} onClick={() => act('resume')}>{busy('resume') ? 'Resuming…' : 'Resume'}</Button><Button type="button" disabled={resolve.isPending} onClick={() => act('retry')}>{busy('retry') ? 'Retrying…' : 'Retry'}</Button><Button type="button" disabled={resolve.isPending} onClick={() => act('cancel')}>{busy('cancel') ? 'Cancelling…' : 'Cancel work'}</Button></>}</div>{resolve.isError && <p role="alert">{resolve.error instanceof Error ? resolve.error.message : 'Could not resolve recovery gate.'}</p>}</div>} />;
}

function AuthorityGateItem({ issue, epic }: { issue: FactoryIssue; epic?: EpicRef }) {
	const resolve = useResolveFactoryAuthorityGate();
	const gate = issue.authority!;
	const pendingAction = gate.resolution === 'approve_pending' ? 'approve' : gate.resolution === 'reject_pending' ? 'reject' : undefined;
	// Only the clicked button reports the in-flight request; the others share the mutation.
	const busy = (action: string) => resolve.isPending && resolve.variables?.action === action;
	return <FactoryDataRow id={issue.id} epic={epic} title={<strong>{issue.title}</strong>} detail={<strong>Allow {gate.permission}{gate.target && ` on ${gate.target}`}?</strong>} actions={<div><div className="factory-inbox-actions">{pendingAction ? <Button type="button" variant="accent" disabled={resolve.isPending} onClick={() => resolve.mutate({ id: gate.issueId, action: pendingAction })}>{busy(pendingAction) ? 'Retrying…' : `Retry ${pendingAction}`}</Button> : <><Button type="button" variant="accent" disabled={resolve.isPending} onClick={() => resolve.mutate({ id: gate.issueId, action: 'approve' })}>{busy('approve') ? 'Approving…' : 'Approve'}</Button><Button type="button" disabled={resolve.isPending} onClick={() => resolve.mutate({ id: gate.issueId, action: 'reject' })}>{busy('reject') ? 'Rejecting…' : 'Reject'}</Button></>}</div>{resolve.isError && <p role="alert">{resolve.error instanceof Error ? resolve.error.message : 'Could not resolve permission escalation.'}</p>}</div>} />;
}

function ProjectGateItem({ issue, epic }: { issue: FactoryIssue; epic?: EpicRef }) {
	const resolve = useResolveFactoryProjectGate();
	const gate = issue.projectRequest!;
	const [acknowledge, setAcknowledge] = useState(false);
	const [response, setResponse] = useState(gate.response ?? 'Continue without this project.');
	return <FactoryDataRow id={issue.id} epic={epic} title={<strong>Project scope expansion</strong>} detail={<><strong>{gate.requestedProject}</strong><span>{gate.reason}</span></>} actions={<div className="factory-inbox-control"><label><input type="checkbox" checked={acknowledge} onChange={(event) => setAcknowledge(event.target.checked)} />Allow Factory agents to run commands in this project</label><label>Rejection response<input value={response} onChange={(event) => setResponse(event.target.value)} /></label><div className="factory-inbox-actions"><Button type="button" variant="accent" disabled={!acknowledge || resolve.isPending} onClick={() => resolve.mutate({ id: gate.issueId, action: 'approve', response: '', acknowledge: true })}>{resolve.variables?.action === 'approve' && resolve.isPending ? 'Approving…' : 'Approve and replan'}</Button><Button type="button" disabled={resolve.isPending} onClick={() => resolve.mutate({ id: gate.issueId, action: 'reject', response, acknowledge: false })}>{resolve.variables?.action === 'reject' && resolve.isPending ? 'Rejecting…' : 'Reject and resume'}</Button></div>{resolve.isError && <p role="alert">{resolve.error instanceof Error ? resolve.error.message : 'Could not resolve project request.'}</p>}</div>} />;
}

function FailedWorkItem({ issue, epic }: { issue: FactoryIssue; epic?: EpicRef }) {
	const reopen = useReopenFactoryIssue();
	const helpID = useId();
	const guidance = issue.outcomeReason?.includes('no supported Factory delivery remote')
		? 'Add a GitHub or Forgejo git remote (preferably origin), authenticate ocman with GITHUB_TOKEN, FORGEJO_TOKEN/GITEA_TOKEN, or the gh/tea CLI, restart ocman if its environment changed, then reopen this work.'
		: undefined;
	return <FactoryDataRow id={issue.id} epic={epic} title={<strong>{issue.title}</strong>} detail={<><strong>Work {issue.outcome}{issue.retryAttempts ? ` after ${issue.retryAttempts} attempts` : ''}</strong>{issue.outcomeReason && <span>{issue.outcomeReason}</span>}{guidance && <><button type="button" className="factory-error-help-trigger" popoverTarget={helpID}>How to resolve</button><div id={helpID} className="factory-error-help" popover="auto"><strong>Configure a delivery remote</strong><p>{guidance}</p></div></>}</>} actions={<div><div className="factory-inbox-actions"><InvestigateUnblockButton issue={issue} /><Button type="button" disabled={reopen.isPending} onClick={() => reopen.mutate({ epicId: issue.epicId, issueId: issue.id })}>{reopen.isPending ? 'Reopening…' : 'Reopen'}</Button></div>{reopen.isError && <p role="alert">{reopen.error instanceof Error ? reopen.error.message : 'Could not reopen work.'}</p>}</div>} />;
}

function InvestigateUnblockButton({ issue }: { issue: FactoryIssue }) {
	const investigate = useInvestigateFactoryUnblock();
	const navigate = useNavigate();
	return <><Button type="button" variant="accent" disabled={investigate.isPending} onClick={() => investigate.mutate({ epicId: issue.epicId, issueId: issue.id }, { onSuccess: (session) => navigate(`/session/${encodeURIComponent(session.id)}?factoryEpic=${encodeURIComponent(issue.epicId)}`) })}>{investigate.isPending ? 'Investigating…' : 'Investigate unblock'}</Button>{investigate.isError && <p role="alert">{investigate.error instanceof Error ? investigate.error.message : 'Could not start unblock investigation.'}</p>}</>;
}

function MaterializationItem({ issue, epic }: { issue: FactoryIssue; epic?: EpicRef }) {
	const materialize = useMaterializeFactoryPlan();
	return <FactoryDataRow id={issue.id} epic={epic} title={<strong>{issue.title}</strong>} detail={<>Plan approved, no work graph yet<span>Materialize the approved plan, or add work through Manage graph.</span></>} actions={<div><div className="factory-inbox-actions"><Button type="button" variant="accent" disabled={materialize.isPending} onClick={() => materialize.mutate({ epicId: issue.epicId, issueId: issue.id })}>{materialize.isPending ? 'Materializing…' : 'Materialize plan'}</Button></div>{materialize.isError && <p role="alert">{materialize.error instanceof Error ? materialize.error.message : 'Could not materialize plan.'}</p>}</div>} />;
}

function PlanningItem({ issue, epic }: { issue: FactoryIssue; epic?: EpicRef }) {
	const claim = useClaimFactoryPlan(issue.epicId);
	const navigate = useNavigate();
	return <FactoryDataRow id={issue.id} epic={epic} title={<strong>{issue.title}</strong>} detail="Ready for planning" actions={<div><Button type="button" variant="accent" disabled={claim.isPending} onClick={() => claim.mutate(issue.id, { onSuccess: ({ session }) => navigate(`/session/${encodeURIComponent(session.id)}?factoryEpic=${encodeURIComponent(issue.epicId)}`) })}>{claim.isPending ? 'Claiming plan…' : 'Claim plan'}</Button>{claim.isError && <p role="alert">{claim.error instanceof Error ? claim.error.message : 'Could not claim planning work.'}</p>}</div>} />;
}

export function FactoryEpics() {
  const epics = useWorkEpics();
  const [query, setQuery] = useState('');
	const [status, setStatus] = useState('active');
	const [project, setProject] = useState('');
  const [creating, setCreating] = useState(false);
	const deferredQuery = useDeferredValue(query.trim().toLowerCase());
	const epicProjects = (epic: FactoryEpic) => epic.projects?.map(({ path }) => path) ?? [epic.initialProject];
	const projects = [...new Set(epics.data?.flatMap(epicProjects) ?? [])].sort();
	const filtered = epics.data?.filter((epic) => `${epic.id} ${epic.goal} ${epicProjects(epic).join(' ')}`.toLowerCase().includes(deferredQuery) && (!project || epicProjects(epic).includes(project))) ?? [];
	const closedCount = filtered.filter((epic) => isClosed(epic.status)).length;
	const visible = filtered.filter((epic) => status === 'all' || (status === 'closed' ? isClosed(epic.status) : !isClosed(epic.status)));
	const groups = [...new Set(visible.map((epic) => epic.status))].sort((a, b) => ['open', 'paused', 'closed'].indexOf(a) - ['open', 'paused', 'closed'].indexOf(b));
  return <FactoryPage>
    {epics.isLoading && <p role="status">Loading epics…</p>}
    {epics.isError && <QueryError error={epics.error} retry={() => void epics.refetch()} />}
    <h2>Epics</h2>
		<InventoryToolbar label="Find epics" value={query} onChange={setQuery}>
			<label>Epic status<SelectField value={status} onChange={(event) => setStatus(event.target.value)}><option value="active">Open only</option><option value="all">Open + closed</option><option value="closed">Closed only</option></SelectField></label>
			<label>Epic project<SelectField value={project} onChange={(event) => setProject(event.target.value)}><option value="">All projects</option>{projects.map((path) => <option key={path} value={path}>{path}</option>)}</SelectField></label>
			<span className="factory-result-count" aria-live="polite">{visible.length} shown{status === 'active' && closedCount > 0 ? ` · ${closedCount} closed hidden` : status === 'all' && closedCount > 0 ? ` · ${closedCount} closed` : ''}</span>
			<Button type="button" variant="accent" onClick={() => setCreating(true)}>New epic</Button>
		</InventoryToolbar>
    {creating && <Drawer title="Create epic" onClose={() => setCreating(false)}><CreateEpic onCreated={() => setCreating(false)} /></Drawer>}
    {!epics.isLoading && !epics.isError && !visible.length && <p className="oc-empty">No epics match this search.</p>}
		{!!visible.length && <div className="factory-list" aria-label="Epics">{groups.map((group) => { const items = visible.filter((epic) => epic.status === group); const label = statusLabel(group); return <DataTableGroup key={group} label={label} noun="epics" count={items.length} markerClassName={`factory-status-dot--${label.toLowerCase().replaceAll(' ', '-')}`}>{items.map((epic) => <DataTableRow key={epic.id} className="factory-grid-row" primary={<Link to={`/factory/epics/${encodeURIComponent(epic.id)}`}>{epic.goal}</Link>} secondary={<span className="factory-list-id">{epic.id}</span>} meta={<><div className="factory-cell" data-testid="cell-project">{epicProjects(epic).map((path) => <Link key={path} to={`/project/${encodeURIComponent(path)}`}><ProjectLabel path={path} /></Link>)}</div><div className="factory-cell" data-testid="epic-progress"><span>{epic.progress?.requiredSucceeded ?? 0}/{epic.progress?.requiredTotal ?? 0}</span><progress value={epic.progress?.requiredSucceeded ?? 0} max={epic.progress?.requiredTotal || 1} aria-label="Required issues done" /></div></>} />)}</DataTableGroup>; })}</div>}
  </FactoryPage>;
}

export function FactoryOverview() {
	const [actionQuery, setActionQuery] = useState('');
	const [actionType, setActionType] = useState('all');
	const deferredActionQuery = useDeferredValue(actionQuery.trim().toLowerCase());
	const epics = useWorkEpics();
	const queue = useFactoryQueue();
	const sessions = useSessions();
	const issueQueries = useFactoryGraphIssues(epics.data);
	const sessionByID = new Map((sessions.data ?? []).map((session) => [session.id, session]));
	const epicByID = new Map((epics.data ?? []).map((epic) => [epic.id, epic]));
	const epicGoal = (epicID: string) => epicByID.get(epicID)?.goal ?? epicID;
	const planGates = epics.data?.filter((epic) => epic.planGate?.resolution === 'open') ?? [];
	const issues = issueQueries.flatMap((result) => result.data ?? []);
	const issuesLoading = issueQueries.some((result) => result.isLoading);
	const issueError = issueQueries.find((result) => result.isError);
	const allRecoveryGates = issues.filter((issue) => issue.recovery && issue.recovery.resolution !== 'resume' && issue.recovery.resolution !== 'retry' && issue.recovery.resolution !== 'cancel');
	const allAuthorityGates = issues.filter((issue) => issue.authority && issue.authority.resolution !== 'approve' && issue.authority.resolution !== 'reject');
	const allProjectGates = issues.filter((issue) => issue.projectRequest && !['approved', 'rejected'].includes(issue.projectRequest.resolution));
	const openEpics = new Set(epics.data?.filter((epic) => epic.status === 'open').map((epic) => epic.id));
	const running = queue.data?.filter((item) => item.state === 'running') ?? [];
	const runningAttemptIDs = new Set(running.map((item) => item.attemptId));
	const planning = epics.data?.flatMap((epic) => (epic.attempts ?? []).filter((attempt) => (attempt.phase === 'prepared' || attempt.phase === 'active' || attempt.phase === 'stopping') && !runningAttemptIDs.has(attempt.id)).map((attempt) => ({ epic, attempt }))) ?? [];
	const allReadyPlans = issues.filter((issue) => openEpics.has(issue.epicId) && issue.kind === 'plan' && issue.dispatchState === 'ready' && !planning.some(({ attempt }) => attempt.workId === issue.id));
	const allFailedWork = issues.filter((issue) => openEpics.has(issue.epicId) && ['task', 'implementation', 'delivery'].includes(issue.kind) && issue.status === 'closed' && (issue.outcome === 'failed' || issue.outcome === 'cancelled'));
	const allBlockedWork = issues.filter((issue) => openEpics.has(issue.epicId) && issue.dispatchState === 'terminally_blocked' && !allFailedWork.some((failed) => failed.epicId === issue.epicId));
	const allMaterializations = issues.filter((issue) => openEpics.has(issue.epicId) && issue.kind === 'materialization' && issue.dispatchState === 'ready');
	// Stuck epics with an actionable row above are already covered; this catches the dead-ends nothing else surfaces.
	const allStuck = epics.data?.filter((epic) => epic.progress?.stuck && !allFailedWork.some((issue) => issue.epicId === epic.id) && !allBlockedWork.some((issue) => issue.epicId === epic.id) && !allMaterializations.some((issue) => issue.epicId === epic.id)) ?? [];
	// ponytail: answering live prompts stays on the session page.
	const allPrompts = [...new Map([...running.map((item) => ({ session: sessionByID.get(item.session?.id ?? ''), epic: epicByID.get(item.epicId), issueID: item.id, issueTitle: item.title })), ...planning.map(({ epic, attempt }) => ({ session: sessionByID.get(attempt.session.id), epic, issueID: attempt.workId, issueTitle: 'Planning' }))].filter((item): item is { session: Session; epic: FactoryEpic | undefined; issueID: string; issueTitle: string } => Boolean(item.session?.pendingPermission || item.session?.pendingQuestion)).map((item) => [item.session.id, item])).values()];
	const matchesAction = (type: string, ...values: Array<string | undefined>) => (actionType === 'all' || actionType === type) && values.join(' ').toLowerCase().includes(deferredActionQuery);
	const readyPlans = allReadyPlans.filter((issue) => matchesAction('planning', epicGoal(issue.epicId), issue.id, issue.title));
	const visiblePlanGates = planGates.filter((epic) => matchesAction('review', epic.goal, epic.id, epic.planGate?.issueId));
	const recoveryGates = allRecoveryGates.filter((issue) => matchesAction('recovery', epicGoal(issue.epicId), issue.id, issue.title, issue.recovery?.question, issue.recovery?.reason));
	const authorityGates = allAuthorityGates.filter((issue) => matchesAction('permission', epicGoal(issue.epicId), issue.id, issue.title, issue.authority?.permission, issue.authority?.target));
	const projectGates = allProjectGates.filter((issue) => matchesAction('project', epicGoal(issue.epicId), issue.id, issue.projectRequest?.requestedProject, issue.projectRequest?.reason));
	const failedWork = allFailedWork.filter((issue) => matchesAction('failed', epicGoal(issue.epicId), issue.id, issue.title, issue.outcomeReason));
	const blockedWork = allBlockedWork.filter((issue) => matchesAction('blocked', epicGoal(issue.epicId), issue.id, issue.title, issue.outcomeReason));
	const materializations = allMaterializations.filter((issue) => matchesAction('materialization', epicGoal(issue.epicId), issue.id, issue.title));
	const stuck = allStuck.filter((epic) => matchesAction('stuck', epic.goal, epic.id, ...(epic.progress?.closureBlockers ?? [])));
	const prompts = allPrompts.filter((item) => matchesAction(item.session.pendingPermission ? 'permission' : 'prompt', item.epic?.goal, item.issueID, item.issueTitle, item.session.title));
	const inboxTotal = allReadyPlans.length + planGates.length + allRecoveryGates.length + allAuthorityGates.length + allProjectGates.length + allPrompts.length + allFailedWork.length + allBlockedWork.length + allMaterializations.length + allStuck.length;
	const inboxCount = readyPlans.length + visiblePlanGates.length + recoveryGates.length + authorityGates.length + projectGates.length + prompts.length + failedWork.length + blockedWork.length + materializations.length + stuck.length;
	const liveStatus = (sessionID?: string) => { const session = sessionID ? sessionByID.get(sessionID) : undefined; return session && session.status !== 'done' ? <StatusBadge status={session.status} pending={session.pendingPermission || session.pendingQuestion} /> : null; };
	return <FactoryPage>
		<h2>Action inbox</h2>
		<InventoryToolbar label="Find actions" value={actionQuery} onChange={setActionQuery}><label>Action type<SelectField value={actionType} onChange={(event) => setActionType(event.target.value)}><option value="all">All actions</option><option value="planning">Planning</option><option value="review">Plan review</option><option value="project">Project scope</option><option value="recovery">Recovery</option><option value="permission">Permission</option><option value="prompt">Agent prompt</option><option value="failed">Failed work</option><option value="blocked">Blocked work</option><option value="materialization">Materialization</option><option value="stuck">Stuck epic</option></SelectField></label><span className="factory-result-count" aria-live="polite">{inboxCount} action{inboxCount === 1 ? '' : 's'}</span></InventoryToolbar>
		{epics.isLoading && <p role="status">Loading epics…</p>}
		{epics.isError && <QueryError error={epics.error} retry={() => void epics.refetch()} />}
		{issuesLoading && <p role="status">Loading action inbox…</p>}
		{issueError && <QueryError error={issueError.error} retry={() => void issueError.refetch()} />}
		{!epics.isLoading && !epics.isError && !issuesLoading && !issueError && !inboxCount && <p className="oc-empty">{inboxTotal ? 'No actions match these filters.' : 'Nothing needs your attention.'}</p>}
		{!!inboxCount && <div className="factory-list factory-list--actions" aria-label="Action inbox"><DataTableGroup label="Needs attention" noun="actions" count={inboxCount} markerClassName="factory-status-dot--blocked">
			{readyPlans.map((issue) => <PlanningItem key={issue.id} issue={issue} epic={epicByID.get(issue.epicId)} />)}
			{visiblePlanGates.map((epic) => <FactoryDataRow key={epic.id} id={epic.planGate!.issueId} epic={epic} title={<strong>Plan approval</strong>} detail={`Revision ${epic.planGate!.proposalRevision}`} actions={<Link to={`/factory/epics/${encodeURIComponent(epic.id)}`}>Review plan</Link>} />)}
			{recoveryGates.map((issue) => <RecoveryGateItem key={issue.id} issue={issue} epic={epicByID.get(issue.epicId)} />)}
			{authorityGates.map((issue) => <AuthorityGateItem key={issue.id} issue={issue} epic={epicByID.get(issue.epicId)} />)}
			{projectGates.map((issue) => <ProjectGateItem key={issue.id} issue={issue} epic={epicByID.get(issue.epicId)} />)}
			{failedWork.map((issue) => <FailedWorkItem key={issue.id} issue={issue} epic={epicByID.get(issue.epicId)} />)}
			{blockedWork.map((issue) => <FactoryDataRow key={issue.id} id={issue.id} epic={epicByID.get(issue.epicId)} title={<strong>{issue.title}</strong>} detail={<><strong>Blocked: a prerequisite failed</strong><span><DispatchExplanation item={issue} /></span></>} actions={<InvestigateUnblockButton issue={issue} />} />)}
			{materializations.map((issue) => <MaterializationItem key={issue.id} issue={issue} epic={epicByID.get(issue.epicId)} />)}
			{stuck.map((epic) => <FactoryDataRow key={`stuck-${epic.id}`} id={epic.id} idLabel="Epic" title={<strong>{epic.goal}</strong>} detail={<>Stuck: nothing can proceed<span>Closure blocked by: {epic.progress.closureBlockers?.join(', ')}</span></>} actions={<Link to={`/factory/epics/${encodeURIComponent(epic.id)}`}>Manage graph</Link>} />)}
			{prompts.map(({ session, epic, issueID, issueTitle }) => <FactoryDataRow key={session.id} id={issueID} epic={epic} title={<strong>{issueTitle}</strong>} detail={<>Agent is waiting for you: {session.title}<span>{session.pendingPermission ? 'Permission prompt' : 'Question'}</span></>} actions={<Link to={`/session/${encodeURIComponent(session.id)}`}>Answer in session</Link>} />)}
		</DataTableGroup></div>}
		<h2>Live work</h2>
		{queue.isLoading && <p role="status">Loading live work…</p>}
		{queue.isError && <QueryError error={queue.error} retry={() => void queue.refetch()} />}
		{!queue.isLoading && !queue.isError && !running.length && !planning.length && <p className="oc-empty">No agents are working right now.</p>}
		{(!!running.length || !!planning.length) && <div className="factory-list factory-list--actions" aria-label="Live work"><DataTableGroup label="In progress" noun="work items" count={running.length + planning.length} markerClassName="factory-status-dot--in-progress">
			{planning.map(({ epic, attempt }) => <FactoryDataRow key={attempt.id} id={attempt.workId} epic={epic} title={<strong>Planning</strong>} detail={liveStatus(attempt.session.id) ?? attempt.phase} actions={attempt.session.id && <Link to={`/session/${encodeURIComponent(attempt.session.id)}?factoryEpic=${encodeURIComponent(epic.id)}`} aria-label={`Open session ${attempt.session.id}`}>Open session</Link>} />)}
			{running.map((item) => <FactoryDataRow key={item.id} id={item.id} epic={epicByID.get(item.epicId)} title={<strong>{item.title}</strong>} detail={liveStatus(item.session?.id) ?? item.state} actions={item.session?.id && <Link to={`/session/${encodeURIComponent(item.session.id)}`} aria-label={`Open session ${item.session.id}`}>Open session</Link>} />)}
		</DataTableGroup></div>}
	</FactoryPage>;
}

export function FactoryHowTo() {
	return <FactoryPage>
		<article className="factory-how-to">
			<div className="factory-how-to-intro"><p>From a goal to reviewed work</p><h2>How Factory works</h2><p>Factory turns a goal into a planned graph of coding-agent work, runs that work within configured capacity, and brings decisions back to you.</p></div>
			<ol>
				<li><span className="factory-how-to-step" aria-hidden="true">1</span><div><h3>Create an epic</h3><p>Open <Link to="/factory/epics">Epics</Link>, choose <strong>New epic</strong>, then provide the outcome, supporting context, starting project, and Formula. A Formula defines the shape of the initial work.</p></div></li>
				<li><span className="factory-how-to-step" aria-hidden="true">2</span><div><h3>Review the plan</h3><p>The planning agent proposes the work graph: issues, dependencies, and the projects involved. The plan appears in the <Link to="/factory/overview">Overview</Link> action inbox. Approve it, request a revision with feedback, or reject it.</p></div></li>
				<li><span className="factory-how-to-step" aria-hidden="true">3</span><div><h3>Let Factory execute</h3><p>After approval, Factory materializes the graph and dispatches ready issues. Dependencies control order, while global and per-project capacity limit parallel work. Follow active and waiting work in the <Link to="/factory/queue">Queue</Link>.</p></div></li>
				<li><span className="factory-how-to-step" aria-hidden="true">4</span><div><h3>Handle decisions</h3><p>Factory pauses when it needs plan approval, a permission decision, an answer, or recovery from failed work. These requests collect in the action inbox. Agent prompts open in their session; graph-level decisions stay on the Factory page.</p></div></li>
				<li><span className="factory-how-to-step" aria-hidden="true">5</span><div><h3>Review the result</h3><p>Use <Link to="/factory/issues">Issues</Link> to inspect work and outcomes. Required work must succeed before its container can close. Optional work does not block closure, but any unfinished optional work remains visible.</p></div></li>
			</ol>
			<section className="factory-how-to-map"><h3>Where to look</h3><div><Link to="/factory/overview"><strong>Overview</strong><span>Human decisions and currently running agents.</span></Link><Link to="/factory/epics"><strong>Epics</strong><span>Goals, plans, progress, and work graphs.</span></Link><Link to="/factory/issues"><strong>Issues</strong><span>Every unit of work and its outcome.</span></Link><Link to="/factory/queue"><strong>Queue</strong><span>Dispatch order, blockers, retries, and active work.</span></Link><Link to="/factory/configuration"><strong>Configuration</strong><span>Execution capacity and reusable Formula revisions.</span></Link></div></section>
		</article>
	</FactoryPage>;
}

export function FactoryEpicDetail() {
  const { id = '' } = useParams();
  const epic = useWorkEpic(id);
	const implementation = useFactoryImplementationModel(epic.data);
  const pour = usePourFactoryEpic(id);
	const proposals = useFactoryProposals(id);
  const decideGate = useDecideFactoryPlanGate(id);
	const closeMol = useCloseFactoryMol(id);
  const closeEpic = useCloseFactoryEpic(id);
	const setPaused = useSetFactoryEpicPaused(id);
	const allEpics = useWorkEpics();
	const graphIssueQueries = useFactoryGraphIssues(allEpics.data);
	const graphIssues = useFactoryIssues(id);
	const removedIssues = useFactoryRemovedIssues(id);
	const [feedback, setFeedback] = useState('');
	const [gateStatus, setGateStatus] = useState('');
	const [started, setStarted] = useState(false);
	const [managing, setManaging] = useState(false);
	const [tab, setTab] = useState<'board' | 'graph' | 'plan'>();
  const proposalHistory = proposals.data ?? (epic.data?.proposal ? [epic.data.proposal] : []);
  if (epic.isLoading) return <FactoryPage><p role="status">Loading epic…</p></FactoryPage>;
  if (epic.isError) return <FactoryPage><QueryError error={epic.error} retry={() => void epic.refetch()} /></FactoryPage>;
  if (!epic.data) return <FactoryPage><p className="oc-empty">Epic not found.</p></FactoryPage>;
	const progress = epic.data.progress ?? { requiredTotal: 0, requiredSucceeded: 0, optionalOpen: 0 };
	const rootMolID = graphIssues.data?.find((issue) => issue.kind === 'mol' && !issue.parentId)?.id;
	// Until the user picks a tab, an open plan gate wins: reviewing the manifest is
	// the only thing that can move the epic forward.
	const active = tab ?? (epic.data.planGate?.resolution === 'open' ? 'plan' : 'board');
	// The gate decides one exact revision; draw that one, not whatever is newest.
	const gatedProposal = proposalHistory.find((proposal) => proposal.revision === epic.data?.planGate?.proposalRevision);
	const close = async () => {
		try {
			// ponytail: the root Mol is a container the user never sees; close it on the way out.
			// A failure here is not reported: the epic guard below turns it into the accurate 409.
			if (rootMolID) await closeMol.mutateAsync(rootMolID).catch(() => {});
			await closeEpic.mutateAsync(false);
		} catch (error) {
			if (!(error instanceof Error) || (error as Error & { status?: number }).status !== 409 || !window.confirm('This epic still has unfinished work. Close it anyway?')) return;
			closeEpic.mutate(true);
		}
	};
  return <FactoryPage>
    <h2>{epic.data.goal}</h2>
		<FactoryStartedToast open={started} onOpenChange={setStarted} />
		<dl className="factory-epic-details"><div><dt>Status</dt><dd data-testid="epic-status">{epic.data.status}</dd></div><div><dt>Projects</dt><dd>{(epic.data.projects ?? [{ path: epic.data.initialProject }]).map(({ path }, index) => <span key={path}>{index > 0 && ', '}<ProjectLabel path={path} /></span>)}</dd></div></dl>
    {/* ponytail: every epic action lives here, above the proposal dumps that used to push them off screen. */}
    <section className="factory-epic-actions" aria-label="Epic actions">
      {epic.data.planGate?.resolution === 'open' && <div className="factory-epic-gate" aria-label="Plan approval gate"><h3>Plan approval</h3><p>Revision {epic.data.planGate.proposalRevision}: {epic.data.planGate.proposalHash}</p>{gatedProposal && <div aria-label="Proposed plan"><EpicGraph issues={proposalIssues(gatedProposal.manifest)} preview />{gatedProposal.rationaleMarkdown && <details className="factory-proposal"><summary>Rationale</summary><MarkdownContent text={gatedProposal.rationaleMarkdown} /></details>}</div>}<FactoryImplementationModel {...implementation} /><label>Feedback<textarea value={feedback} onChange={(event) => setFeedback(event.target.value)} /></label><div className="factory-epic-action-row">{(['approve', 'revise', 'reject'] as const).map((action) => <Button key={action} type="button" variant={action === 'approve' ? 'accent' : 'default'} disabled={decideGate.isPending || (action === 'approve' && implementation.loading)} onClick={() => decideGate.mutate({ action, expectedRevision: epic.data!.planGate!.proposalRevision, expectedHash: epic.data!.planGate!.proposalHash, feedback, ...(action === 'approve' && implementation.model && { implementationModel: implementation.model }) }, { onSuccess: () => { if (action === 'approve') { setGateStatus(''); setStarted(true); } else setGateStatus(action === 'revise' ? 'Revision requested.' : 'Plan rejected.'); } })}>{action === 'approve' ? 'Approve plan' : action === 'revise' ? 'Request revision' : 'Reject plan'}</Button>)}</div>{decideGate.isError && <p role="alert">{decideGate.error instanceof Error ? decideGate.error.message : 'Could not decide Plan gate.'}</p>}</div>}
      {epic.data.planGate?.resolution === 'revision_requested' && <div className="factory-epic-gate" aria-label="Plan approval gate"><h3>Plan approval</h3><p role="status">Revision requested. Waiting for a new Plan proposal.</p><Button type="button" disabled={epic.isFetching || proposals.isFetching} onClick={() => { setGateStatus(''); void Promise.all([epic.refetch(), proposals.refetch()]); }}>{epic.isFetching || proposals.isFetching ? 'Checking…' : 'Check for new proposal'}</Button></div>}
      {gateStatus && <p role="status">{gateStatus}</p>}
      <div className="factory-epic-action-row">
        <Button type="button" variant="accent" onClick={() => pour.mutate()} disabled={pour.isPending}>{pour.isPending ? 'Pouring…' : 'Pour graph'}</Button>
        <Button type="button" onClick={() => void close()} disabled={closeEpic.isPending || closeMol.isPending}>Close epic</Button>
        {epic.data.status !== 'closed' && <Button type="button" onClick={() => setPaused.mutate(epic.data!.status !== 'paused')} disabled={setPaused.isPending}>{epic.data.status === 'paused' ? 'Resume epic' : 'Pause epic'}</Button>}
      </div>
		<p>Required work: {progress.requiredSucceeded}/{progress.requiredTotal} complete. Optional work open: {progress.optionalOpen}.</p>
		{!!progress.projectDeliveries?.length && <ul aria-label="Project deliveries" className="factory-issues">{progress.projectDeliveries.map((delivery) => <li key={delivery.issueId ?? `${delivery.project}:pending`}><ProjectLabel path={delivery.project} /><span>{delivery.status === 'ready_for_review' ? 'Ready for review' : delivery.status.replaceAll('_', ' ').replace(/^./, (value) => value.toUpperCase())}</span></li>)}</ul>}
		{!!progress.closureBlockers?.length && <p>Closure blocked by: {progress.closureBlockers.join(', ')}</p>}
      {pour.isError && <p role="alert">{pour.error instanceof Error ? pour.error.message : 'Could not pour graph.'}</p>}
      {(closeEpic.isError || setPaused.isError) && <p role="alert">{(closeEpic.error ?? setPaused.error) instanceof Error ? (closeEpic.error ?? setPaused.error)!.message : 'Could not update epic.'}</p>}
    </section>
    <div className="factory-tabs" role="tablist" aria-label="Epic views">
      {(['board', 'graph', 'plan'] as const).map((value) => <button key={value} type="button" role="tab" aria-selected={active === value} className={`factory-tab${active === value ? ' active' : ''}`} onClick={() => setTab(value)}>{value === 'board' ? 'Board' : value === 'graph' ? 'Graph' : 'Plan'}</button>)}
    </div>
    {active === 'board' && <section role="tabpanel" aria-label="Board">
      <div className="factory-toolbar"><h3>Issues</h3><Button type="button" onClick={() => setManaging(true)} disabled={!graphIssues.data}>Manage graph</Button></div>
      <IssueList epicID={id} />
      {!!removedIssues.data?.length && <section aria-label="Removed work audit"><h3>Removed work audit</h3><ul className="factory-issues">{removedIssues.data.map((issue) => <li key={issue.id}><strong>{issue.title}</strong><span>{issue.kind} · Removed {issue.removedAt ? new Date(issue.removedAt).toISOString() : 'previously'}</span><span>Audit reference: {issue.id}</span></li>)}</ul></section>}
    </section>}
    {active === 'graph' && <section role="tabpanel" aria-label="Graph"><EpicGraph issues={graphIssues.data} /></section>}
    {active === 'plan' && <section role="tabpanel" aria-label="Plan">
      <PlanningAttempts epicID={id} attempts={epic.data.attempts ?? []} />
      {proposals.isError && <QueryError error={proposals.error} retry={() => void proposals.refetch()} />}
      {!proposals.isError && !proposalHistory.length && <p className="oc-empty">No plan has been proposed yet.</p>}
      {proposalHistory.map((proposal) => <details key={proposal.revision} className="factory-proposal"><summary>Proposal revision: {proposal.revision}</summary><p>Content hash: {proposal.contentHash}</p><pre>{JSON.stringify(proposal.manifest, null, 2)}</pre>{proposal.rationaleMarkdown && <MarkdownContent text={proposal.rationaleMarkdown} />}</details>)}
    </section>}
    {managing && graphIssues.data && <Drawer title="Manage graph" onClose={() => setManaging(false)}><GraphControls epicID={id} issues={graphIssues.data} allIssues={graphIssueQueries.flatMap((query) => query.data ?? [])} /></Drawer>}
  </FactoryPage>;
}

function PlanningAttempts({ epicID, attempts }: { epicID: string; attempts: FactoryAttempt[] }) {
	if (!attempts.length) return null;
	// ponytail: attempts arrive in creation order, so index+1 is the human-facing attempt number.
	const current = attempts.find((attempt) => attempt.phase === 'active') ?? attempts[attempts.length - 1];
	const earlier = attempts.filter((attempt) => attempt !== current);
	const row = (attempt: FactoryAttempt) => <>Attempt {attempts.indexOf(attempt) + 1} · {attempt.phase === 'active' ? 'Running' : 'Finished'}{attempt.session.id && <> · <Link to={`/session/${encodeURIComponent(attempt.session.id)}?factoryEpic=${encodeURIComponent(epicID)}`}>Open session</Link></>}</>;
	return <section aria-label="Planning"><h3>Planning</h3><p>{row(current)}</p>{!!earlier.length && <details><summary>{earlier.length} earlier attempt{earlier.length === 1 ? '' : 's'}</summary><ol>{earlier.map((attempt) => <li key={attempt.id}>{row(attempt)}</li>)}</ol></details>}</section>;
}

function InventoryToolbar({ label, value, onChange, children }: { label: string; value: string; onChange: (value: string) => void; children?: ReactNode }) {
  return <div className="factory-toolbar factory-filter-bar" role="search"><label>{label}<SearchField value={value} onChange={(event) => onChange(event.target.value)} /></label>{children}</div>;
}

function QueueTable({ label, items, epicByID }: { label: string; items: FactoryQueueItem[]; epicByID: Map<string, FactoryEpic> }) {
  const marker = label === 'Active work' ? 'factory-status-dot--in-progress' : label === 'Waiting work' ? 'factory-status-dot--waiting' : '';
  return <DataTableGroup label={label} noun="items" count={items.length} markerClassName={marker}>{items.map((item) => <DataTableRow key={item.id} className="factory-grid-row" primary={<strong>{item.title}</strong>} secondary={<><span className="oc-data-table-field-label">Issue </span><Link to={`/factory/epics/${encodeURIComponent(item.epicId)}`}>{item.id}</Link></>} meta={<><ProjectCell path={item.project} /><EpicCell id={item.epicId} goal={epicByID.get(item.epicId)?.goal} /><div className="factory-row-detail"><DispatchExplanation item={{ ...item, dispatchState: item.state }} />{item.session?.id && <Link to={`/session/${encodeURIComponent(item.session.id)}`} aria-label={`Open session ${item.session.id}`}>Open session</Link>}</div></>} />)}</DataTableGroup>;
}

export function FactoryQueue() {
  const queue = useFactoryQueue();
  const epicByID = new Map((useWorkEpics().data ?? []).map((epic) => [epic.id, epic]));
  const capacity = useFactoryCapacityPolicy();
  const active = queue.data?.filter((item) => item.state === 'running') ?? [];
  const next = queue.data?.filter((item) => item.state === 'ready') ?? [];
  const waiting = queue.data?.filter((item) => item.state !== 'running' && item.state !== 'ready' && item.state !== 'completed') ?? [];
	const visible = active.length + next.length + waiting.length;
  return <FactoryPage>
    <h2>Execution queue</h2>
		{capacity.isLoading && <p role="status">Loading capacity…</p>}
		{capacity.isError && <QueryError error={capacity.error} retry={() => void capacity.refetch()} />}
    {capacity.data && <p className="factory-capacity">Capacity: {capacity.data.globalCapacity} global, {capacity.data.projectCapacity} per project.</p>}
    {queue.isLoading && <p role="status">Loading execution queue…</p>}
    {queue.isError && <QueryError error={queue.error} retry={() => void queue.refetch()} />}
    {!queue.isLoading && !queue.isError && !visible && <p className="oc-empty">No implementation work is active or waiting.</p>}
    {!!visible && <div className="factory-list factory-list--queue" aria-label="Execution queue">{!!active.length && <QueueTable label="Active work" items={active} epicByID={epicByID} />}{!!next.length && <QueueTable label="Next up" items={next} epicByID={epicByID} />}{!!waiting.length && <QueueTable label="Waiting work" items={waiting} epicByID={epicByID} />}</div>}
  </FactoryPage>;
}

export function FactoryConfiguration() {
	const formula = useFactoryFormula(TRACER_FORMULA_ID, 1);
	const formulas = useFactoryFormulas();
	const validateFormula = useValidateFactoryFormula();
	const previewFormula = usePreviewFactoryFormula();
	const saveFormula = useSaveFactoryFormula();
	const capacity = useFactoryCapacityPolicy();
	const saveCapacity = useSetFactoryCapacityPolicy();
	const [error, setError] = useState('');
	const [capacityError, setCapacityError] = useState('');
	const [formulaErrors, setFormulaErrors] = useState<string[]>([]);
	const [formulaSaved, setFormulaSaved] = useState('');
	const [selectedFormula, setSelectedFormula] = useState('new');
	const policy = capacity.data;
	const inspectedFormula = formulas.data?.find((item) => `${item.id}@${item.version}` === selectedFormula);
	async function save(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		const form = new FormData(event.currentTarget);
		try {
			const projectOverrides = JSON.parse(String(form.get('projectOverrides'))) as Record<string, number>;
			if (!projectOverrides || Array.isArray(projectOverrides)) throw new Error('Project capacity overrides must be a JSON object.');
			await saveCapacity.mutateAsync({ globalCapacity: Number(form.get('globalCapacity')), projectCapacity: Number(form.get('projectCapacity')), projectOverrides });
			setCapacityError('');
		} catch (reason) { setCapacityError(reason instanceof Error ? reason.message : 'Could not save capacity policy.'); }
	}
	async function saveFormulaRevision(formElement: HTMLFormElement, action: 'validate' | 'preview' | 'save') {
		if (!formElement.reportValidity()) return;
		const form = new FormData(formElement); const source = String(form.get('source')); const id = String(form.get('id')).trim();
		try {
			if (action === 'validate') setFormulaErrors((await validateFormula.mutateAsync({ id, source })).errors ?? [])
			else if (action === 'preview') setFormulaErrors((await previewFormula.mutateAsync({ id, source })).errors ?? [])
			else { const saved = await saveFormula.mutateAsync({ id, source }); setFormulaErrors([]); setFormulaSaved(`Formula saved: ${saved.id}@${saved.version}`) }
			setError('');
		} catch (reason) { setError(reason instanceof Error ? reason.message : 'Formula is invalid.'); }
	}
	return <FactoryPage>
		<h2>Factory configuration</h2>
		{capacity.isLoading && <p role="status">Loading capacity policy…</p>}
		{capacity.isError && <QueryError error={capacity.error} retry={() => void capacity.refetch()} />}
		{policy && <form onSubmit={(event) => void save(event)}>
			<label>Global implementation capacity<input aria-label="Global implementation capacity" name="globalCapacity" type="number" min="1" max="1000" required defaultValue={policy.globalCapacity} /></label>
			<label>Default project implementation capacity<input aria-label="Default project implementation capacity" name="projectCapacity" type="number" min="1" max="1000" required defaultValue={policy.projectCapacity} /></label>
			<label>Project capacity overrides (JSON)<textarea aria-label="Project capacity overrides (JSON)" name="projectOverrides" defaultValue={JSON.stringify(policy.projectOverrides, null, 2)} /></label>
			<button type="submit" disabled={saveCapacity.isPending}>{saveCapacity.isPending ? 'Saving…' : 'Save capacity policy'}</button>
		</form>}
		{capacityError && <p role="alert">{capacityError}</p>}
		{error && <p role="alert">{error}</p>}
		{formula.isLoading && <p role="status">Loading Formula…</p>}
		{formula.isError && <QueryError error={formula.error} retry={() => void formula.refetch()} />}
		{formula.data && <section>
			<h3>{formula.data.name} · {formula.data.id}@{formula.data.version}</h3>
			<p>Content hash: {formula.data.hash}</p>
			<p>Source hash: {formula.data.sourceHash}</p>
			<p role="status">Formula is {formula.data.valid ? 'valid' : 'invalid'}</p>
			<label>Tracer Formula source<textarea aria-label="Tracer Formula source" readOnly value={formula.data.source} /></label>
			<h4>Graph</h4>
			<p>Inputs: {formula.data.inputs.join(', ')}</p>
			<ul>{formula.data.nodes.map((node) => <li key={node.key}>{node.key} · {node.kind}</li>)}</ul>
			<ul>{formula.data.edges.map((edge, index) => <li key={`${edge.from}-${edge.to}-${edge.type ?? 'blocks'}-${index}`}>{edge.from} → {edge.to}</li>)}</ul>
		</section>}
		<section>
			<h3>Custom Formula revisions</h3>
			{formulas.isError && <QueryError error={formulas.error} retry={() => void formulas.refetch()} />}
			<label>Formula<select aria-label="Formula" value={selectedFormula} onChange={(event) => setSelectedFormula(event.target.value)}><option value="new">New Formula</option>{formulas.data?.filter((item) => item.id !== TRACER_FORMULA_ID).map((item) => <option key={`${item.id}@${item.version}`} value={`${item.id}@${item.version}`}>{item.name} · {item.id}@{item.version}</option>)}</select></label>
			{inspectedFormula && <section aria-label="Formula inspection"><p>Content hash: {inspectedFormula.hash}</p><p>Source hash: {inspectedFormula.sourceHash}</p><pre>{JSON.stringify(inspectedFormula.compiled, null, 2)}</pre><label>Stored Formula source<textarea aria-label="Stored Formula source" readOnly value={inspectedFormula.source} /></label></section>}
			<form onSubmit={(event) => { event.preventDefault(); void saveFormulaRevision(event.currentTarget, 'save'); }}>
				<label>Custom Formula ID<input aria-label="Custom Formula ID" name="id" required pattern="custom/[a-z][a-z0-9_-]*" /></label>
				<label>Custom Formula TOML<textarea aria-label="Custom Formula TOML" name="source" required defaultValue={'version = 1\nname = "My Formula"\n\n[[input]]\nkey = "goal"\n\n[[input]]\nkey = "initial_project"\n\n[[issue]]\nkey = "plan"\nkind = "plan"\n'} /></label>
				<button type="button" onClick={(event) => { if (event.currentTarget.form) void saveFormulaRevision(event.currentTarget.form, 'validate'); }} disabled={validateFormula.isPending}>Validate TOML</button>
				<button type="button" onClick={(event) => { if (event.currentTarget.form) void saveFormulaRevision(event.currentTarget.form, 'preview'); }} disabled={previewFormula.isPending}>Preview Formula</button>
				<button type="submit" disabled={saveFormula.isPending}>{saveFormula.isPending ? 'Saving…' : 'Save immutable revision'}</button>
			</form>
			{validateFormula.data && <p role="status">Formula is {validateFormula.data.valid ? 'valid' : 'invalid'}{validateFormula.data.valid && `: ${validateFormula.data.hash}`}</p>}
			{previewFormula.data && <pre aria-label="Formula preview">{JSON.stringify(previewFormula.data.compiled, null, 2)}</pre>}
			{previewFormula.data && <p role="status">Preview {previewFormula.data.valid ? `valid: ${previewFormula.data.hash}` : 'invalid'}</p>}
			{!!formulaErrors.length && <section role="alert" aria-label="Formula diagnostics"><p>Formula diagnostics</p><ul>{formulaErrors.map((diagnostic, index) => <li key={`${diagnostic}-${index}`}>{diagnostic}</li>)}</ul></section>}
			{formulaSaved && <p role="status">{formulaSaved}</p>}
		</section>
	</FactoryPage>;
}

function FactoryPage({ children }: { children: ReactNode }) {
  return <main className="factory-page"><nav aria-label="Factory"><NavLink to="/factory/overview">Overview</NavLink><NavLink to="/factory/epics">Epics</NavLink><NavLink to="/factory/issues">Issues</NavLink><NavLink to="/factory/queue">Queue</NavLink><NavLink to="/factory/configuration">Configuration</NavLink><NavLink to="/factory/how-to" className={({ isActive }) => `factory-how-to-link${isActive ? ' active' : ''}`}><i className="bi bi-book" aria-hidden="true" />How to</NavLink></nav>{children}</main>;
}
