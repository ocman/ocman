import { useDeferredValue, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { Link, NavLink, useParams } from 'react-router-dom';
import { MarkdownContent } from '../components/assistant/MarkdownText';
import { Button, SelectField } from '../components/Control';
import { SearchSelect } from '../components/SearchSelect';
import { StatusBadge } from '../components/StatusBadge';
import { useCloseFactoryEpic, useCloseFactoryMol, useCreateWorkEpic, useDecideFactoryPlanGate, useFactoryCapacityPolicy, useFactoryFormula, useFactoryFormulas, useFactoryGraphIssues, useFactoryIssues, useFactoryProposals, useFactoryQueue, useFactoryRemovedIssues, useMaterializeFactoryPlan, useMutateFactoryGraph, usePourFactoryEpic, usePreviewFactoryFormula, useProjects, useReopenFactoryIssue, useResolveFactoryAuthorityGate, useResolveFactoryRecoveryGate, useSaveFactoryFormula, useSessions, useSetFactoryCapacityPolicy, useValidateFactoryFormula, useWorkEpic, useWorkEpics } from '../lib/queries';
import type { FactoryAttempt, FactoryFormula, FactoryGraphMutation, FactoryIssue, FactoryQueueItem, Session } from '../lib/api';

const TRACER_FORMULA_ID = 'ocman/tracer';
import { Modal } from '../components/Modal';
import { IssueDrawer } from './FactoryIssues';
import './Factory.css';

function QueryError({ error, retry }: { error: unknown; retry: () => void }) {
  return <div className="oc-error-banner" role="alert">
    {error instanceof Error ? error.message : 'Factory data is unavailable.'}
    <Button type="button" onClick={retry}>Retry</Button>
  </div>;
}

function IssueList({ epicID, query = '' }: { epicID: string; query?: string }) {
  const issues = useFactoryIssues(epicID);
  const [selected, setSelected] = useState<FactoryIssue>();
  if (issues.isLoading) return <p role="status">Loading issues…</p>;
  if (issues.isError) return <QueryError error={issues.error} retry={() => void issues.refetch()} />;
  const visible = issues.data?.filter((issue) => `${issue.id} ${issue.title} ${issue.kind} ${issue.status}`.toLowerCase().includes(query)) ?? [];
  if (!visible.length) return <p className="oc-empty">{issues.data?.length ? 'No issues match this search.' : 'This epic has no issues yet.'}</p>;
  const columns = [
    ['backlog', 'Backlog'],
    ['blocked', 'Blocked'],
    ['in_progress', 'In progress'],
    ['closed', 'Done'],
    ['other', 'Other'],
  ] as const;
  const status = (issue: FactoryIssue) => {
    const normalized = { open: 'backlog', completed: 'closed' }[issue.status] ?? issue.status;
    return columns.some(([key]) => key === normalized) ? normalized : 'other';
  };
  return <><div className="factory-kanban" aria-label="Epic issues by status">{columns.map(([key, label]) => <section key={key} className="factory-kanban-column" aria-label={`${label} issues`}><h4>{label}</h4><ul>{visible.filter((issue) => status(issue) === key).map((issue) => <li key={issue.id}><button type="button" onClick={() => setSelected(issue)} aria-label={`Open issue ${issue.id}`}><strong data-testid={`issue-title-${issue.id}`}>{issue.title}</strong><span>{issue.id} · {issue.kind}</span></button></li>)}</ul></section>)}</div>{selected && <IssueDrawer key={selected.id} issue={selected} onClose={() => setSelected(undefined)} />}</>;
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
    case 'not_applicable': return <span>Dispatch: not applicable because the recovery condition was not met{hasBlocker && <> ({blocker})</>}.</span>;
    case 'deferred': return <span>Dispatch: delayed{item.outcomeReason ? `: ${item.outcomeReason}` : ''}.</span>;
    case 'retry_wait': return <span>Dispatch: retry {item.retryAttempts ?? 0} scheduled for {item.retryAt ? new Date(item.retryAt).toISOString() : 'a later time'}.</span>;
    case 'waiting': return <span>Dispatch: waiting for prerequisites{hasBlocker && <>: {blocker}</>}.</span>;
    case 'ready': return <span>Dispatch: ready.</span>;
    case 'running': return <span>Dispatch: in progress.</span>;
    case 'completed': return <span>Dispatch: complete.</span>;
    case 'reference': return <span>Dispatch: reference work is not scheduled.</span>;
    default: return null;
  }
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
      {action === 'create' && <><label>Kind<select name="kind"><option value="task">Task</option><option value="implementation">Implementation</option><option value="mol">Mol</option></select></label><label>Title<input aria-label="Work title" name="title" required /></label><label>Description<textarea aria-label="Work description" name="description" /></label></>}
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
    const key = JSON.stringify([goal, brief, initialProject, selectedFormula]);
    if (pendingInstantiation.current?.key !== key) {
      pendingInstantiation.current = { key, id: crypto.randomUUID() };
    }
    try {
      await create.mutateAsync({
        instantiationId: pendingInstantiation.current.id,
        goal,
        brief: brief || undefined,
        initialProject,
				acknowledgeLocalExecution,
        ...(formula && { formulaId, formulaRevision: Number(formulaRevision) }),
      });
      pendingInstantiation.current = undefined;
      formElement.reset();
      setInitialProject('');
      setFormula('');
      setError('');
      onCreated?.();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not create epic.');
    }
  }
  const selectedFormula = formulas.data?.find((item) => `${item.id}@${item.version}` === formula);
  return <form className="factory-create" onSubmit={(event) => void submit(event)}>
    <div className="factory-field"><label>Goal<input name="goal" required aria-describedby="factory-goal-help" /></label><p id="factory-goal-help">The outcome this Factory work should deliver.</p></div>
    <div className="factory-field"><label>Brief<textarea name="brief" aria-describedby="factory-brief-help" /></label><p id="factory-brief-help">Optional context, constraints, and decisions for the planning work.</p></div>
    <div className="factory-field"><label>Initial Factory project<SearchSelect value={initialProject} ariaLabel="Initial Factory project" placeholder={projects.isLoading ? 'Loading projects…' : 'Select a project'} searchLabel="Search projects" disabled={projects.isLoading || !projects.data?.some((project) => !project.archived)} onChange={(value) => { setInitialProject(value); setError(''); }} options={projects.data?.filter((project) => !project.archived).map((project) => ({ value: project.directory, label: project.directory })) ?? []} /></label><p>The local repository where Factory starts work. Commands run on this machine.</p></div>
    {projects.isError && <p role="alert">Could not load Factory projects.</p>}
    <div className="factory-field"><label>Formula<SelectField name="formula" value={formula} onChange={(event) => setFormula(event.target.value)} aria-describedby="factory-formula-help"><option value="">Built-in tracer</option>{formulas.data?.filter((item) => item.id !== TRACER_FORMULA_ID).map((item) => <option key={`${item.id}@${item.version}`} value={`${item.id}@${item.version}`}>{item.name} · {item.id}@{item.version}</option>)}</SelectField></label><p id="factory-formula-help">Defines the initial work graph. Formula revisions are immutable.</p><p aria-live="polite">{describeFormula(selectedFormula)}</p></div>
		<label><input type="checkbox" name="acknowledgeLocalExecution" />Allow Factory agents to run commands in this repository</label>
    <Button type="submit" variant="accent" disabled={create.isPending}>{create.isPending ? 'Creating…' : 'Create epic'}</Button>
    {error && <p role="alert">{error}</p>}
  </form>;
}

function RecoveryGateItem({ issue, epic }: { issue: FactoryIssue; epic: string }) {
	const resolve = useResolveFactoryRecoveryGate();
	const gate = issue.recovery!;
	const [response, setResponse] = useState(gate.choices?.[0] ?? '');
	const act = (action: 'resume' | 'retry' | 'cancel') => resolve.mutate({ id: gate.issueId, action, response });
	return <tr>
		<td><strong>{epic}</strong></td>
		<td className="factory-table-id">{issue.id}<span>{issue.title}</span></td>
		<td><strong>{gate.question || issue.title}</strong>{gate.reason && <span>{gate.reason}</span>}</td>
		<td><div className="factory-inbox-control">{gate.choices?.length ? <label>Response<select aria-label={`Recovery response for ${issue.id}`} value={response} onChange={(event) => setResponse(event.target.value)}>{gate.choices.map((choice) => <option key={choice} value={choice}>{choice}</option>)}</select></label> : <label>Response<input aria-label={`Recovery response for ${issue.id}`} value={response} onChange={(event) => setResponse(event.target.value)} /></label>}<div className="factory-inbox-actions"><Button type="button" variant="accent" disabled={resolve.isPending} onClick={() => act('resume')}>Resume</Button><Button type="button" disabled={resolve.isPending} onClick={() => act('retry')}>Retry</Button><Button type="button" disabled={resolve.isPending} onClick={() => act('cancel')}>Cancel work</Button></div>{resolve.isError && <p role="alert">{resolve.error instanceof Error ? resolve.error.message : 'Could not resolve recovery gate.'}</p>}</div></td>
	</tr>;
}

function AuthorityGateItem({ issue, epic }: { issue: FactoryIssue; epic: string }) {
	const resolve = useResolveFactoryAuthorityGate();
	const gate = issue.authority!;
	const pendingAction = gate.resolution === 'approve_pending' ? 'approve' : gate.resolution === 'reject_pending' ? 'reject' : undefined;
	return <tr>
		<td><strong>{epic}</strong></td>
		<td className="factory-table-id">{issue.id}<span>{issue.title}</span></td>
		<td><strong>Allow {gate.permission}{gate.target && ` on ${gate.target}`}?</strong></td>
		<td><div className="factory-inbox-actions">{pendingAction ? <Button type="button" variant="accent" disabled={resolve.isPending} onClick={() => resolve.mutate({ id: gate.issueId, action: pendingAction })}>Retry {pendingAction}</Button> : <><Button type="button" variant="accent" disabled={resolve.isPending} onClick={() => resolve.mutate({ id: gate.issueId, action: 'approve' })}>Approve</Button><Button type="button" disabled={resolve.isPending} onClick={() => resolve.mutate({ id: gate.issueId, action: 'reject' })}>Reject</Button></>}</div>{resolve.isError && <p role="alert">{resolve.error instanceof Error ? resolve.error.message : 'Could not resolve permission escalation.'}</p>}</td>
	</tr>;
}

function FailedWorkItem({ issue, epic }: { issue: FactoryIssue; epic: string }) {
	const reopen = useReopenFactoryIssue();
	return <tr>
		<td><strong>{epic}</strong></td>
		<td className="factory-table-id">{issue.id}<span>{issue.title}</span></td>
		<td><strong>Work {issue.outcome}{issue.retryAttempts ? ` after ${issue.retryAttempts} attempts` : ''}</strong>{issue.outcomeReason && <span>{issue.outcomeReason}</span>}</td>
		<td><div className="factory-inbox-actions"><Button type="button" variant="accent" disabled={reopen.isPending} onClick={() => reopen.mutate({ epicId: issue.epicId, issueId: issue.id })}>Reopen</Button></div>{reopen.isError && <p role="alert">{reopen.error instanceof Error ? reopen.error.message : 'Could not reopen work.'}</p>}</td>
	</tr>;
}

function MaterializationItem({ issue, epic }: { issue: FactoryIssue; epic: string }) {
	const materialize = useMaterializeFactoryPlan();
	return <tr>
		<td><strong>{epic}</strong></td>
		<td className="factory-table-id">{issue.id}<span>{issue.title}</span></td>
		<td><strong>Plan approved, no work graph yet</strong><span>Materialize the approved plan, or add work through Manage graph.</span></td>
		<td><div className="factory-inbox-actions"><Button type="button" variant="accent" disabled={materialize.isPending} onClick={() => materialize.mutate({ epicId: issue.epicId, issueId: issue.id })}>Materialize plan</Button></div>{materialize.isError && <p role="alert">{materialize.error instanceof Error ? materialize.error.message : 'Could not materialize plan.'}</p>}</td>
	</tr>;
}

export function FactoryEpics() {
  const epics = useWorkEpics();
  const [query, setQuery] = useState('');
  const [creating, setCreating] = useState(false);
  const deferredQuery = useDeferredValue(query.trim().toLowerCase());
  const visible = epics.data?.filter((epic) => `${epic.id} ${epic.goal} ${epic.initialProject}`.toLowerCase().includes(deferredQuery)) ?? [];
  return <FactoryPage>
    {epics.isLoading && <p role="status">Loading epics…</p>}
    {epics.isError && <QueryError error={epics.error} retry={() => void epics.refetch()} />}
    <h2>Epics</h2>
    <InventoryToolbar label="Find epics" value={query} onChange={setQuery}><Button type="button" variant="accent" onClick={() => setCreating(true)}>New epic</Button></InventoryToolbar>
    {creating && <Drawer title="Create epic" onClose={() => setCreating(false)}><CreateEpic onCreated={() => setCreating(false)} /></Drawer>}
    {!epics.isLoading && !epics.isError && !visible.length && <p className="oc-empty">No epics match this search.</p>}
    {!!visible.length && <div className="factory-table-wrap"><table className="factory-table" aria-label="Epics"><thead><tr><th>Goal</th><th>Project</th><th>Status</th><th>Progress</th></tr></thead><tbody>{visible.map((epic) => <tr key={epic.id}><td><Link to={`/factory/epics/${encodeURIComponent(epic.id)}`}>{epic.goal}</Link></td><td>{epic.initialProject}</td><td>{epic.status}</td><td>Closure: required {epic.progress?.requiredSucceeded ?? 0}/{epic.progress?.requiredTotal ?? 0} complete. Optional work open: {epic.progress?.optionalOpen ?? 0}.{!!epic.progress?.closureBlockers?.length && <span>Closure blocked by: {epic.progress.closureBlockers.join(', ')}</span>}</td></tr>)}</tbody></table></div>}
  </FactoryPage>;
}

export function FactoryOverview() {
	const epics = useWorkEpics();
	const queue = useFactoryQueue();
	const sessions = useSessions();
	const issueQueries = useFactoryGraphIssues(epics.data);
	const sessionByID = new Map((sessions.data ?? []).map((session) => [session.id, session]));
	const epicGoal = (epicID: string) => epics.data?.find((epic) => epic.id === epicID)?.goal ?? epicID;
	const planGates = epics.data?.filter((epic) => epic.planGate?.resolution === 'open') ?? [];
	const issues = issueQueries.flatMap((result) => result.data ?? []);
	const issuesLoading = issueQueries.some((result) => result.isLoading);
	const issueError = issueQueries.find((result) => result.isError);
	const recoveryGates = issues.filter((issue) => issue.recovery?.resolution === 'open');
	const authorityGates = issues.filter((issue) => issue.authority && issue.authority.resolution !== 'approve' && issue.authority.resolution !== 'reject');
	const openEpics = new Set(epics.data?.filter((epic) => epic.status === 'open').map((epic) => epic.id));
	const failedWork = issues.filter((issue) => openEpics.has(issue.epicId) && (issue.kind === 'task' || issue.kind === 'implementation') && issue.status === 'closed' && (issue.outcome === 'failed' || issue.outcome === 'cancelled'));
	const materializations = issues.filter((issue) => openEpics.has(issue.epicId) && issue.kind === 'materialization' && issue.dispatchState === 'ready');
	// Stuck epics with an actionable row above are already covered; this catches the dead-ends nothing else surfaces.
	const stuck = epics.data?.filter((epic) => epic.progress?.stuck && !failedWork.some((issue) => issue.epicId === epic.id) && !materializations.some((issue) => issue.epicId === epic.id)) ?? [];
	const running = queue.data?.filter((item) => item.state === 'running') ?? [];
	const runningAttemptIDs = new Set(running.map((item) => item.attemptId));
	const planning = epics.data?.flatMap((epic) => (epic.attempts ?? []).filter((attempt) => (attempt.phase === 'prepared' || attempt.phase === 'active' || attempt.phase === 'stopping') && !runningAttemptIDs.has(attempt.id)).map((attempt) => ({ epic, attempt }))) ?? [];
	// ponytail: answering live prompts stays on the session page.
	const prompts = [...new Map([...running.map((item) => ({ session: sessionByID.get(item.session?.id ?? ''), epic: epicGoal(item.epicId), issueID: item.id, issueTitle: item.title })), ...planning.map(({ epic, attempt }) => ({ session: sessionByID.get(attempt.session.id), epic: epic.goal, issueID: attempt.workId, issueTitle: 'Planning' }))].filter((item): item is { session: Session; epic: string; issueID: string; issueTitle: string } => Boolean(item.session?.pendingPermission || item.session?.pendingQuestion)).map((item) => [item.session.id, item])).values()];
	const inboxCount = planGates.length + recoveryGates.length + authorityGates.length + prompts.length + failedWork.length + materializations.length + stuck.length;
	const liveStatus = (sessionID?: string) => { const session = sessionID ? sessionByID.get(sessionID) : undefined; return session ? <StatusBadge status={session.status} pending={session.pendingPermission || session.pendingQuestion} /> : null; };
	return <FactoryPage>
		<h2>Action inbox</h2>
		{epics.isLoading && <p role="status">Loading epics…</p>}
		{epics.isError && <QueryError error={epics.error} retry={() => void epics.refetch()} />}
		{issuesLoading && <p role="status">Loading action inbox…</p>}
		{issueError && <QueryError error={issueError.error} retry={() => void issueError.refetch()} />}
		{!epics.isLoading && !epics.isError && !issuesLoading && !issueError && !inboxCount && <p className="oc-empty">Nothing needs your attention.</p>}
		{!!inboxCount && <div className="factory-table-wrap"><table className="factory-table factory-inbox" aria-label="Action inbox"><thead><tr><th>Epic</th><th>Issue ID</th><th>Status</th><th>Actions</th></tr></thead><tbody>
			{planGates.map((epic) => <tr key={epic.id}><td><strong>{epic.goal}</strong></td><td className="factory-table-id">{epic.planGate!.issueId}<span>Plan approval</span></td><td>Revision {epic.planGate!.proposalRevision}</td><td><Link to={`/factory/epics/${encodeURIComponent(epic.id)}`}>Review plan</Link></td></tr>)}
			{recoveryGates.map((issue) => <RecoveryGateItem key={issue.id} issue={issue} epic={epicGoal(issue.epicId)} />)}
			{authorityGates.map((issue) => <AuthorityGateItem key={issue.id} issue={issue} epic={epicGoal(issue.epicId)} />)}
			{failedWork.map((issue) => <FailedWorkItem key={issue.id} issue={issue} epic={epicGoal(issue.epicId)} />)}
			{materializations.map((issue) => <MaterializationItem key={issue.id} issue={issue} epic={epicGoal(issue.epicId)} />)}
			{stuck.map((epic) => <tr key={`stuck-${epic.id}`}><td><strong>{epic.goal}</strong></td><td className="factory-table-id">{epic.id}<span>Epic</span></td><td><strong>Stuck: nothing can proceed</strong><span>Closure blocked by: {epic.progress.closureBlockers?.join(', ')}</span></td><td><Link to={`/factory/epics/${encodeURIComponent(epic.id)}`}>Manage graph</Link></td></tr>)}
			{prompts.map(({ session, epic, issueID, issueTitle }) => <tr key={session.id}><td><strong>{epic}</strong></td><td className="factory-table-id">{issueID}<span>{issueTitle}</span></td><td>Agent is waiting for you: {session.title}<span>{session.pendingPermission ? 'Permission prompt' : 'Question'}</span></td><td><Link to={`/session/${encodeURIComponent(session.id)}`}>Answer in session</Link></td></tr>)}
		</tbody></table></div>}
		<h2>Live work</h2>
		{queue.isLoading && <p role="status">Loading live work…</p>}
		{queue.isError && <QueryError error={queue.error} retry={() => void queue.refetch()} />}
		{!queue.isLoading && !queue.isError && !running.length && !planning.length && <p className="oc-empty">No agents are working right now.</p>}
		{(!!running.length || !!planning.length) && <div className="factory-table-wrap"><table className="factory-table" aria-label="Live work"><thead><tr><th>Epic</th><th>Issue ID</th><th>Status</th><th>Actions</th></tr></thead><tbody>
			{planning.map(({ epic, attempt }) => <tr key={attempt.id}><td><strong>Planning: {epic.goal}</strong></td><td className="factory-table-id">{attempt.workId}<span>Planning</span></td><td>{liveStatus(attempt.session.id) ?? attempt.phase}</td><td>{attempt.session.id && <Link to={`/session/${encodeURIComponent(attempt.session.id)}`} aria-label={`Open session ${attempt.session.id}`}>Open session</Link>}</td></tr>)}
			{running.map((item) => <tr key={item.id}><td><strong>{epicGoal(item.epicId)}</strong></td><td className="factory-table-id">{item.id}<span>{item.title}</span></td><td>{liveStatus(item.session?.id) ?? item.state}</td><td>{item.session?.id && <Link to={`/session/${encodeURIComponent(item.session.id)}`} aria-label={`Open session ${item.session.id}`}>Open session</Link>}</td></tr>)}
		</tbody></table></div>}
	</FactoryPage>;
}

export function FactoryHowTo() {
	return <FactoryPage>
		<article className="factory-how-to">
			<div className="factory-how-to-intro"><p>From a goal to reviewed work</p><h2>How Factory works</h2><p>Factory turns a goal into a planned graph of coding-agent work, runs that work within configured capacity, and brings decisions back to you.</p></div>
			<ol>
				<li><span className="factory-how-to-step" aria-hidden="true">1</span><div><h3>Create an epic</h3><p>Open <Link to="/factory/epics">Epics</Link>, choose <strong>New epic</strong>, then provide the outcome, supporting context, starting repository, and Formula. A Formula defines the shape of the initial work.</p></div></li>
				<li><span className="factory-how-to-step" aria-hidden="true">2</span><div><h3>Review the plan</h3><p>The planning agent proposes the work graph: issues, dependencies, and the repositories involved. The plan appears in the <Link to="/factory/overview">Overview</Link> action inbox. Approve it, request a revision with feedback, or reject it.</p></div></li>
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
  const pour = usePourFactoryEpic(id);
	const proposals = useFactoryProposals(id);
  const decideGate = useDecideFactoryPlanGate(id);
	const closeMol = useCloseFactoryMol(id);
  const closeEpic = useCloseFactoryEpic(id);
	const allEpics = useWorkEpics();
	const graphIssueQueries = useFactoryGraphIssues(allEpics.data);
	const graphIssues = useFactoryIssues(id);
	const removedIssues = useFactoryRemovedIssues(id);
	const [feedback, setFeedback] = useState('');
	const [gateStatus, setGateStatus] = useState('');
	const [managing, setManaging] = useState(false);
  const proposalHistory = proposals.data ?? (epic.data?.proposal ? [epic.data.proposal] : []);
  if (epic.isLoading) return <FactoryPage><p role="status">Loading epic…</p></FactoryPage>;
  if (epic.isError) return <FactoryPage><QueryError error={epic.error} retry={() => void epic.refetch()} /></FactoryPage>;
  if (!epic.data) return <FactoryPage><p className="oc-empty">Epic not found.</p></FactoryPage>;
	const progress = epic.data.progress ?? { requiredTotal: 0, requiredSucceeded: 0, optionalOpen: 0 };
	const rootMolID = graphIssues.data?.find((issue) => issue.kind === 'mol' && !issue.parentId)?.id;
	const closureError = closeMol.error ?? closeEpic.error;
  return <FactoryPage>
    <h2>{epic.data.goal}</h2>
    <dl className="factory-epic-details"><div><dt>Status</dt><dd data-testid="epic-status">{epic.data.status}</dd></div><div><dt>Project</dt><dd>{epic.data.initialProject}</dd></div></dl>
    <section aria-label="Closure progress"><p>Required work: {progress.requiredSucceeded}/{progress.requiredTotal} complete. Optional work open: {progress.optionalOpen}.</p>{!!progress.closureBlockers?.length && <p>Closure blocked by: {progress.closureBlockers.join(', ')}</p>}<Button type="button" onClick={() => rootMolID && closeMol.mutate(rootMolID)} disabled={closeMol.isPending || !rootMolID}>Close Mol</Button><Button type="button" onClick={() => closeEpic.mutate()} disabled={closeEpic.isPending}>Close epic</Button>{(closeMol.isError || closeEpic.isError) && <p role="alert">{closureError instanceof Error ? closureError.message : 'Could not close container.'}</p>}</section>
    <PlanningAttempts attempts={epic.data.attempts ?? []} />
    {proposals.isError && <QueryError error={proposals.error} retry={() => void proposals.refetch()} />}
    {proposalHistory.map((proposal) => <section key={proposal.revision}><p>Proposal revision: {proposal.revision}</p><p>Content hash: {proposal.contentHash}</p><pre>{JSON.stringify(proposal.manifest, null, 2)}</pre>{proposal.rationaleMarkdown && <MarkdownContent text={proposal.rationaleMarkdown} />}</section>)}
		{epic.data.planGate?.resolution === 'open' && <section aria-label="Plan approval gate"><h3>Plan approval</h3><p>Revision {epic.data.planGate.proposalRevision}: {epic.data.planGate.proposalHash}</p><label>Feedback<textarea value={feedback} onChange={(event) => setFeedback(event.target.value)} /></label>{(['approve', 'revise', 'reject'] as const).map((action) => <Button key={action} type="button" disabled={decideGate.isPending} onClick={() => decideGate.mutate({ action, expectedRevision: epic.data!.planGate!.proposalRevision, expectedHash: epic.data!.planGate!.proposalHash, feedback }, { onSuccess: () => setGateStatus(action === 'approve' ? 'Plan approved.' : action === 'revise' ? 'Revision requested.' : 'Plan rejected.') })}>{action === 'approve' ? 'Approve plan' : action === 'revise' ? 'Request revision' : 'Reject plan'}</Button>)}{decideGate.isError && <p role="alert">{decideGate.error instanceof Error ? decideGate.error.message : 'Could not decide Plan gate.'}</p>}</section>}
		{epic.data.planGate?.resolution === 'revision_requested' && <section aria-label="Plan approval gate"><h3>Plan approval</h3><p role="status">Revision requested. Waiting for a new Plan proposal.</p><Button type="button" disabled={epic.isFetching || proposals.isFetching} onClick={() => { setGateStatus(''); void Promise.all([epic.refetch(), proposals.refetch()]); }}>{epic.isFetching || proposals.isFetching ? 'Checking…' : 'Check for new proposal'}</Button></section>}
		{gateStatus && <p role="status">{gateStatus}</p>}
    <Button type="button" variant="accent" onClick={() => pour.mutate()} disabled={pour.isPending}>{pour.isPending ? 'Pouring…' : 'Pour graph'}</Button>
    {pour.isError && <p role="alert">{pour.error instanceof Error ? pour.error.message : 'Could not pour graph.'}</p>}
    <div className="factory-toolbar"><h3>Issues</h3><Button type="button" onClick={() => setManaging(true)} disabled={!graphIssues.data}>Manage graph</Button></div>
    <IssueList epicID={id} />
    {managing && graphIssues.data && <Drawer title="Manage graph" onClose={() => setManaging(false)}><GraphControls epicID={id} issues={graphIssues.data} allIssues={graphIssueQueries.flatMap((query) => query.data ?? [])} /></Drawer>}
    {!!removedIssues.data?.length && <section aria-label="Removed work audit"><h3>Removed work audit</h3><ul className="factory-issues">{removedIssues.data.map((issue) => <li key={issue.id}><strong>{issue.title}</strong><span>{issue.kind} · Removed {issue.removedAt ? new Date(issue.removedAt).toISOString() : 'previously'}</span><span>Audit reference: {issue.id}</span></li>)}</ul></section>}
  </FactoryPage>;
}

function PlanningAttempts({ attempts }: { attempts: FactoryAttempt[] }) {
	if (!attempts.length) return null;
	// ponytail: attempts arrive in creation order, so index+1 is the human-facing attempt number.
	const current = attempts.find((attempt) => attempt.phase === 'active') ?? attempts[attempts.length - 1];
	const earlier = attempts.filter((attempt) => attempt !== current);
	const row = (attempt: FactoryAttempt) => <>Attempt {attempts.indexOf(attempt) + 1} · {attempt.phase === 'active' ? 'Running' : 'Finished'}{attempt.session.id && <> · <Link to={`/session/${encodeURIComponent(attempt.session.id)}`}>Open session</Link></>}</>;
	return <section aria-label="Planning"><h3>Planning</h3><p>{row(current)}</p>{!!earlier.length && <details><summary>{earlier.length} earlier attempt{earlier.length === 1 ? '' : 's'}</summary><ol>{earlier.map((attempt) => <li key={attempt.id}>{row(attempt)}</li>)}</ol></details>}</section>;
}

function InventoryToolbar({ label, value, onChange, children }: { label: string; value: string; onChange: (value: string) => void; children?: ReactNode }) {
  return <div className="factory-toolbar" role="search"><label>{label}<input type="search" value={value} onChange={(event) => onChange(event.target.value)} /></label>{children}</div>;
}

function QueueTable({ label, items }: { label: string; items: FactoryQueueItem[] }) {
  return <div className="factory-table-wrap"><table className="factory-table" aria-label={label}><thead><tr><th>Issue</th><th>Epic</th><th>Repository</th><th>Dispatch</th><th>Outcome</th><th>Session</th></tr></thead><tbody>{items.map((item) => <tr key={item.id}>
		<td><strong>{item.title}</strong><span>{item.id}</span></td>
		<td className="factory-table-id">{item.epicId}</td>
		<td>{item.repository}</td>
		<td>{item.state}{item.attemptId && <span>Attempt {item.attemptId}</span>}<DispatchExplanation item={{ ...item, dispatchState: item.state }} /></td>
		<td>{item.outcome || '-'}</td>
		<td>{item.session?.id ? <Link to={`/session/${encodeURIComponent(item.session.id)}`} aria-label={`Open session ${item.session.id}`}>Open session</Link> : '-'}</td>
	</tr>)}</tbody></table></div>;
}

export function FactoryQueue() {
  const queue = useFactoryQueue();
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
    {!!active.length && <section><h3>Active work</h3><QueueTable label="Active work" items={active} /></section>}
    {!!next.length && <section><h3>Next up</h3><QueueTable label="Next up" items={next} /></section>}
    {!!waiting.length && <section><h3>Waiting work</h3><QueueTable label="Waiting work" items={waiting} /></section>}
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
