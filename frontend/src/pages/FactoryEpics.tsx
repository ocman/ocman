import { useDeferredValue, useRef, useState, type FormEvent } from 'react';
import { Link, useParams } from 'react-router-dom';
import { MarkdownContent } from '../components/assistant/MarkdownText';
import { EpicGraph } from './EpicGraph';
import { proposalIssues } from './factoryGraph';
import { Button, ButtonGroup, SelectField } from '../components/Control';
import { SearchSelect } from '../components/SearchSelect';
import { EmptyState } from '../components/EmptyState';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '../components/Tabs';
import { ProjectLabel } from '../components/ProjectLabel';
import { DataTableGroup, DataTableRow } from '../components/DataTable';
import { FactoryStartedToast } from '../components/FactoryStartedToast';
import { FactoryImplementationModel } from '../components/FactoryImplementationModel';
import { useFactoryImplementationModel } from '../components/useFactoryImplementationModel';
import { FactoryEpicModels } from '../components/FactoryEpicModels';
import { useCloseFactoryEpic, useCloseFactoryMol, useCreateWorkEpic, useDecideFactoryPlanGate, useFactoryFormulas, useFactoryGraphIssues, useFactoryIssues, useFactoryProposals, useFactoryRemovedIssues, useMutateFactoryGraph, usePourFactoryEpic, useProjects, useSetFactoryEpicPaused, useWorkEpic, useWorkEpics } from '../lib/queries';
import type { FactoryAttempt, FactoryEpic, FactoryFormula, FactoryGraphMutation, FactoryIssue } from '../lib/api';
import { fuzzyMatch } from '../lib/format';
import { FactoryIssueRow, IssueDrawer } from './FactoryIssues';
import { TRACER_FORMULA_ID, isClosed, newInstantiationID, statusLabel } from './factoryHelpers';
import { Drawer, FactoryPage, InventoryToolbar, QueryError } from './FactoryLayout';

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
	const filtered = inventory.filter((issue) => fuzzyMatch(search, `${issue.id} ${issue.title} ${issue.kind} ${issue.status}`) && (!kind || issue.kind === kind));
	const closedCount = filtered.filter((issue) => isClosed(issue.status)).length;
	const visible = filtered.filter((issue) => statusFilter === 'all' || (statusFilter === 'closed' ? isClosed(issue.status) : !isClosed(issue.status)));
	const statusGroups: Record<string, string> = { in_progress: 'In progress', blocked: 'Blocked', retry_wait: 'Waiting', deferred: 'Waiting', open: 'Open', closed: 'Closed', completed: 'Closed' };
	const groupFor = (issue: FactoryIssue) => statusGroups[issue.status] ?? statusLabel(issue.status);
	const order = ['In progress', 'Blocked', 'Open', 'Waiting', 'Closed'];
	const groups = [...new Set(visible.map(groupFor))].sort((a, b) => order.indexOf(a) - order.indexOf(b));
	return <><InventoryToolbar label="Find board issues" value={query} onChange={setQuery}><label>Board status<SelectField value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}><option value="active">Open only</option><option value="all">Open + closed</option><option value="closed">Closed only</option></SelectField></label><label>Board type<SelectField value={kind} onChange={(event) => setKind(event.target.value)}><option value="">All types</option>{kinds.map((value) => <option key={value} value={value}>{value}</option>)}</SelectField></label><span className="factory-result-count" aria-live="polite">{visible.length} shown{statusFilter === 'active' && closedCount > 0 ? ` · ${closedCount} closed hidden` : statusFilter === 'all' && closedCount > 0 ? ` · ${closedCount} closed` : ''}</span></InventoryToolbar>{!visible.length ? <EmptyState>{inventory.length ? 'No issues match these filters.' : 'This epic has no issues yet.'}</EmptyState> : <div className="factory-list" aria-label="Epic issues by status">{groups.map((group) => { const items = visible.filter((issue) => groupFor(issue) === group); return <DataTableGroup key={group} label={group} noun="issues" count={items.length} markerClassName={`factory-status-dot--${group.toLowerCase().replaceAll(' ', '-')}`}>{items.map((issue) => <FactoryIssueRow key={issue.id} issue={issue} onOpen={() => setSelected(issue)} />)}</DataTableGroup>; })}</div>}{selected && <IssueDrawer key={selected.id} issue={selected} onClose={() => setSelected(undefined)} />}</>;
}

function GraphControls({ epicID, issues, allIssues }: { epicID: string; issues: FactoryIssue[]; allIssues: FactoryIssue[] }) {
  const mutate = useMutateFactoryGraph(epicID);
  const [action, setAction] = useState<FactoryGraphMutation['action']>('create');
  const [confirmed, setConfirmed] = useState(false);
  const [issueID, setIssueID] = useState('');
  const [dependencyType, setDependencyType] = useState<NonNullable<FactoryGraphMutation['dependencyType']>>('blocks');
  const [mutationStatus, setMutationStatus] = useState('');
  const openIssues = issues.filter((issue) => issue.status === 'open');
  const selectedIssueID = issueID || openIssues[0]?.id || '';
  const selectedIssue = openIssues.find((issue) => issue.id === selectedIssueID);
  const selectedDependencyType = dependencyType === 'merge_gated' && selectedIssue?.kind !== 'implementation' && selectedIssue?.kind !== 'task' ? 'blocks' : dependencyType;
  const linkedTargets = new Set(selectedIssue?.dependsOn?.filter((dependency) => dependency.type === selectedDependencyType).map((dependency) => dependency.id));
  const targets = allIssues.filter((issue) => action === 'unlink' ? linkedTargets.has(issue.id) : selectedDependencyType === 'merge_gated' ? issue.kind === 'delivery' && issue.project !== selectedIssue?.project : issue.status === 'open').sort((a, b) => action !== 'unlink' && selectedDependencyType === 'merge_gated' ? (b.createdAt ?? 0) - (a.createdAt ?? 0) || b.id.localeCompare(a.id) : 0);
  const noTarget = (action === 'reparent' && !openIssues.some((issue) => issue.id !== selectedIssueID)) || ((action === 'link' || action === 'unlink') && !targets.some((issue) => issue.id !== selectedIssueID));
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const mutation: FactoryGraphMutation = { action, issueId: selectedIssueID };
    if (action === 'create') Object.assign(mutation, { parentId: selectedIssueID, kind: String(form.get('kind')), title: String(form.get('title')).trim(), description: String(form.get('description')).trim(), requirement: String(form.get('requirement')) });
    if (action === 'edit') Object.assign(mutation, { title: String(form.get('title')).trim(), description: String(form.get('description')).trim() });
    if (action === 'reparent') Object.assign(mutation, { parentId: String(form.get('parentId')), requirement: String(form.get('requirement')) });
    if (action === 'link' || action === 'unlink') Object.assign(mutation, { dependsOnId: String(form.get('dependsOnId')), dependencyType: selectedDependencyType });
    try { await mutate.mutateAsync(mutation); setConfirmed(false); setMutationStatus(action === 'delete' ? 'Work soft-deleted. It remains in Factory audit history.' : 'Graph updated.'); } catch { /* The mutation error is rendered below. */ }
  }
  return <section className="factory-graph-controls" aria-label="Manage graph">
    <p>Only open, unstarted work can change. Dependency targets name their Work Epic.</p>
    {!openIssues.length ? <EmptyState>No eligible work is available.</EmptyState> : <form onSubmit={(event) => void submit(event)}>
      <label>Action<select aria-label="Graph action" value={action} onChange={(event) => { setAction(event.target.value as FactoryGraphMutation['action']); setConfirmed(false); }}><option value="create">Create child work</option><option value="edit">Edit work</option><option value="reparent">Reparent work</option><option value="link">Link dependency</option><option value="unlink">Unlink dependency</option><option value="delete">Soft-delete work</option></select></label>
      <label>{action === 'create' ? 'Parent work' : 'Work'}<select aria-label={action === 'create' ? 'Parent work' : 'Work'} name="issueId" value={selectedIssueID} onChange={(event) => setIssueID(event.target.value)}>{openIssues.map((issue) => <option key={issue.id} value={issue.id}>{issue.title} ({issue.id})</option>)}</select></label>
      {action === 'create' && <><label>Kind<select name="kind"><option value="task">Task</option><option value="implementation">Implementation</option></select></label><label>Title<input aria-label="Work title" name="title" required /></label><label>Description<textarea aria-label="Work description" name="description" /></label></>}
      {action === 'edit' && <><label>Title<input key={`title-${selectedIssueID}`} aria-label="Work title" name="title" required defaultValue={selectedIssue?.title} /></label><label>Description<textarea key={`description-${selectedIssueID}`} aria-label="Work description" name="description" defaultValue={selectedIssue?.description} /></label></>}
      {(action === 'create' || action === 'reparent') && <label>Requirement<select name="requirement"><option value="required">Required</option><option value="optional">Optional</option></select></label>}
      {action === 'reparent' && <label>New parent<select aria-label="New parent" name="parentId">{openIssues.filter((issue) => issue.id !== selectedIssueID).map((issue) => <option key={issue.id} value={issue.id}>{issue.title} ({issue.id})</option>)}</select></label>}
      {(action === 'link' || action === 'unlink') && <><label>Dependency type<select name="dependencyType" value={selectedDependencyType} onChange={(event) => setDependencyType(event.target.value as typeof dependencyType)}><option value="blocks">Blocks</option><option value="on_failure">On failure</option>{(selectedIssue?.kind === 'implementation' || selectedIssue?.kind === 'task') && <option value="merge_gated">Merge gated</option>}</select></label><label>Dependency target<select aria-label="Dependency target" name="dependsOnId">{targets.filter((issue) => issue.id !== selectedIssueID).map((issue) => <option key={issue.id} value={issue.id}>{issue.epicId === epicID ? 'This Work Epic' : `Work Epic ${issue.epicId}`}: {issue.title} ({issue.id})</option>)}</select></label></>}
      {action === 'delete' && <label><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} /> I understand this soft-deletes this work and its descendants.</label>}
      {noTarget && <p role="alert">Select another open work item for this change.</p>}
      <Button type="submit" variant="accent" aria-busy={mutate.isPending} disabled={mutate.isPending || noTarget || (action === 'delete' && !confirmed)}>{mutate.isPending ? 'Saving…' : action === 'delete' ? 'Soft-delete work' : 'Save graph change'}</Button>
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
    <Button type="submit" variant="accent" aria-busy={create.isPending} disabled={create.isPending}>{create.isPending ? 'Creating…' : 'Create epic'}</Button>
    {error && <p role="alert">{error}</p>}
  </form>;
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
	const filtered = epics.data?.filter((epic) => fuzzyMatch(deferredQuery, `${epic.id} ${epic.goal} ${epicProjects(epic).join(' ')}`) && (!project || epicProjects(epic).includes(project))) ?? [];
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
    {!epics.isLoading && !epics.isError && !visible.length && <EmptyState>No epics match this search.</EmptyState>}
		{!!visible.length && <div className="factory-list" aria-label="Epics">{groups.map((group) => { const items = visible.filter((epic) => epic.status === group); const label = statusLabel(group); return <DataTableGroup key={group} label={label} noun="epics" count={items.length} markerClassName={`factory-status-dot--${label.toLowerCase().replaceAll(' ', '-')}`}>{items.map((epic) => <DataTableRow key={epic.id} className="factory-grid-row" primary={<Link to={`/factory/epics/${encodeURIComponent(epic.id)}`}>{epic.goal}</Link>} secondary={<span className="factory-list-id">{epic.id}</span>} meta={<><div className="factory-cell" data-testid="cell-project">{epicProjects(epic).map((path) => <Link key={path} to={`/project/${encodeURIComponent(path)}`}><ProjectLabel path={path} /></Link>)}</div><div className="factory-cell" data-testid="epic-progress"><span>{epic.progress?.requiredSucceeded ?? 0}/{epic.progress?.requiredTotal ?? 0}</span><progress value={epic.progress?.requiredSucceeded ?? 0} max={epic.progress?.requiredTotal || 1} aria-label="Required issues done" /></div></>} />)}</DataTableGroup>; })}</div>}
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
	const [tab, setTab] = useState<string>();
  const proposalHistory = proposals.data ?? (epic.data?.proposal ? [epic.data.proposal] : []);
  if (epic.isLoading) return <FactoryPage><p role="status">Loading epic…</p></FactoryPage>;
  if (epic.isError) return <FactoryPage><QueryError error={epic.error} retry={() => void epic.refetch()} /></FactoryPage>;
  if (!epic.data) return <FactoryPage><EmptyState>Epic not found.</EmptyState></FactoryPage>;
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
      {epic.data.planGate?.resolution === 'open' && <div className="factory-epic-gate" aria-label="Plan approval gate">
        <h3>Plan approval</h3><p>Revision {epic.data.planGate.proposalRevision}: {epic.data.planGate.proposalHash}</p>
        {gatedProposal && <div aria-label="Proposed plan"><EpicGraph issues={proposalIssues(gatedProposal.manifest)} preview />{gatedProposal.rationaleMarkdown && <details className="factory-proposal"><summary>Rationale</summary><MarkdownContent text={gatedProposal.rationaleMarkdown} /></details>}</div>}
        <FactoryImplementationModel {...implementation} />
        <label>Feedback<textarea value={feedback} disabled={decideGate.isPending} placeholder="Changes to request or a reason for rejecting the plan" onChange={(event) => setFeedback(event.target.value)} /></label>
        <ButtonGroup label="Plan approval actions">{([
          ['approve', 'Approve plan', 'Approving…'],
          ['revise', 'Request revision', 'Requesting revision…'],
          ['reject', 'Reject plan', 'Rejecting…'],
        ] as const).map(([action, label, pendingLabel]) => <Button key={action} type="button" variant={action === 'approve' ? 'accent' : 'default'} aria-busy={decideGate.isPending && decideGate.variables?.action === action} disabled={decideGate.isPending || (action === 'approve' && implementation.loading)} onClick={() => decideGate.mutate({ action, expectedRevision: epic.data!.planGate!.proposalRevision, expectedHash: epic.data!.planGate!.proposalHash, feedback, ...(action === 'approve' && implementation.model && { implementationModel: implementation.model }) }, { onSuccess: () => { if (action === 'approve') { setGateStatus(''); setStarted(true); } else setGateStatus(action === 'revise' ? 'Revision requested.' : 'Plan rejected.'); } })}>{decideGate.isPending && decideGate.variables?.action === action ? pendingLabel : label}</Button>)}</ButtonGroup>
        {decideGate.isError && <p role="alert">{decideGate.error instanceof Error ? decideGate.error.message : 'Could not decide Plan gate.'}</p>}
      </div>}
      {epic.data.planGate?.resolution === 'revision_requested' && <div className="factory-epic-gate" aria-label="Plan approval gate"><h3>Plan approval</h3><p role="status">Revision requested. Waiting for a new Plan proposal.</p><Button type="button" disabled={epic.isFetching || proposals.isFetching} onClick={() => { setGateStatus(''); void Promise.all([epic.refetch(), proposals.refetch()]); }}>{epic.isFetching || proposals.isFetching ? 'Checking…' : 'Check for new proposal'}</Button></div>}
      {gateStatus && <p role="status">{gateStatus}</p>}
      <FactoryEpicModels epic={epic.data} />
      <ButtonGroup label="Epic controls">
        <Button type="button" onClick={() => pour.mutate()} aria-busy={pour.isPending} disabled={pour.isPending}>{pour.isPending ? 'Pouring…' : 'Pour graph'}</Button>
        <Button type="button" onClick={() => void close()} aria-busy={closeEpic.isPending || closeMol.isPending} disabled={closeEpic.isPending || closeMol.isPending}>{closeEpic.isPending || closeMol.isPending ? 'Closing…' : 'Close epic'}</Button>
        {epic.data.status !== 'closed' && <Button type="button" onClick={() => setPaused.mutate(epic.data!.status !== 'paused')} aria-busy={setPaused.isPending} disabled={setPaused.isPending}>{setPaused.isPending ? (setPaused.variables ? 'Pausing…' : 'Resuming…') : epic.data.status === 'paused' ? 'Resume epic' : 'Pause epic'}</Button>}
      </ButtonGroup>
		<p>Required work: {progress.requiredSucceeded}/{progress.requiredTotal} complete. Optional work open: {progress.optionalOpen}.</p>
		{!!progress.projectDeliveries?.length && <ul aria-label="Project deliveries" className="factory-issues">{progress.projectDeliveries.map((delivery) => <li key={delivery.issueId ?? `${delivery.project}:pending`}><ProjectLabel path={delivery.project} />{delivery.lineage && <span>Delivery {delivery.lineage}</span>}<span>{delivery.status === 'ready_for_review' ? 'Ready for review' : delivery.status.replaceAll('_', ' ').replace(/^./, (value) => value.toUpperCase())}</span></li>)}</ul>}
		{!!progress.closureBlockers?.length && <p>Closure blocked by: {progress.closureBlockers.join(', ')}</p>}
      {pour.isError && <p role="alert">{pour.error instanceof Error ? pour.error.message : 'Could not pour graph.'}</p>}
      {(closeEpic.isError || setPaused.isError) && <p role="alert">{(closeEpic.error ?? setPaused.error) instanceof Error ? (closeEpic.error ?? setPaused.error)!.message : 'Could not update epic.'}</p>}
    </section>
    <Tabs value={active} onValueChange={setTab} className="factory-epic-views">
    <TabsList aria-label="Epic views">
      <TabsTrigger value="board">Board</TabsTrigger>
      <TabsTrigger value="graph">Graph</TabsTrigger>
      <TabsTrigger value="plan">Plan</TabsTrigger>
    </TabsList>
    <TabsContent value="board" asChild><section>
      <div className="factory-toolbar"><h3>Issues</h3><Button type="button" onClick={() => setManaging(true)} disabled={!graphIssues.data}>Manage graph</Button></div>
      <IssueList epicID={id} />
      {!!removedIssues.data?.length && <section aria-label="Removed work audit"><h3>Removed work audit</h3><ul className="factory-issues">{removedIssues.data.map((issue) => <li key={issue.id}><strong>{issue.title}</strong><span>{issue.kind} · Removed {issue.removedAt ? new Date(issue.removedAt).toISOString() : 'previously'}</span><span>Audit reference: {issue.id}</span></li>)}</ul></section>}
    </section></TabsContent>
    <TabsContent value="graph" asChild><section><EpicGraph issues={graphIssues.data} /></section></TabsContent>
    <TabsContent value="plan" asChild><section>
      <PlanningAttempts epicID={id} attempts={epic.data.attempts ?? []} />
      {proposals.isError && <QueryError error={proposals.error} retry={() => void proposals.refetch()} />}
      {!proposals.isError && !proposalHistory.length && <EmptyState>No plan has been proposed yet.</EmptyState>}
      {proposalHistory.map((proposal) => <details key={proposal.revision} className="factory-proposal"><summary>Proposal revision: {proposal.revision}</summary><p>Content hash: {proposal.contentHash}</p><pre>{JSON.stringify(proposal.manifest, null, 2)}</pre>{proposal.rationaleMarkdown && <MarkdownContent text={proposal.rationaleMarkdown} />}</details>)}
    </section></TabsContent>
    </Tabs>
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
