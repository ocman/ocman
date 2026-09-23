import { useDeferredValue, useId, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Button, SelectField } from '../components/Control';
import { StatusBadge } from '../components/StatusBadge';
import { DataTableGroup, DataTableRow } from '../components/DataTable';
import { useClaimFactoryPlan, useFactoryCapacityPolicy, useFactoryGraphIssues, useFactoryQueue, useInvestigateFactoryUnblock, useMaterializeFactoryPlan, useMutateFactoryGraph, useReopenFactoryIssue, useResolveFactoryAuthorityGate, useResolveFactoryProjectGate, useResolveFactoryRecoveryGate, useSessions, useWorkEpics } from '../lib/queries';
import type { FactoryEpic, FactoryIssue, FactoryQueueItem, Session } from '../lib/api';
import { fuzzyMatch } from '../lib/format';
import { EpicCell, IssueDrawer, ProjectCell, type EpicRef } from './FactoryIssues';
import { OpenIssueContext } from './factoryHelpers';
import { DispatchExplanation, FactoryDataRow, FactoryPage, InventoryToolbar, QueryError } from './FactoryLayout';

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

function WorkflowApprovalItem({ issue, epic }: { issue: FactoryIssue; epic?: EpicRef }) {
	const decide = useMutateFactoryGraph(issue.epicId);
	return <FactoryDataRow id={issue.id} epic={epic} title={<strong>{issue.title}</strong>} detail={issue.workflow?.prompt || 'Workflow approval required'} actions={<div className="factory-inbox-actions"><Button type="button" variant="accent" disabled={decide.isPending} onClick={() => decide.mutate({ action: 'approve_step', issueId: issue.id })}>Approve step</Button><Button type="button" disabled={decide.isPending} onClick={() => decide.mutate({ action: 'reject_step', issueId: issue.id })}>Reject step</Button>{decide.isError && <p role="alert">Could not record workflow approval.</p>}</div>} />;
}

function PlanningItem({ issue, epic }: { issue: FactoryIssue; epic?: EpicRef }) {
	const claim = useClaimFactoryPlan(issue.epicId);
	const navigate = useNavigate();
	return <FactoryDataRow id={issue.id} epic={epic} title={<strong>{issue.title}</strong>} detail="Ready for planning" actions={<div><Button type="button" variant="accent" disabled={claim.isPending} onClick={() => claim.mutate(issue.id, { onSuccess: ({ session }) => navigate(`/session/${encodeURIComponent(session.id)}?factoryEpic=${encodeURIComponent(issue.epicId)}`) })}>{claim.isPending ? 'Claiming plan…' : 'Claim plan'}</Button>{claim.isError && <p role="alert">{claim.error instanceof Error ? claim.error.message : 'Could not claim planning work.'}</p>}</div>} />;
}

export function FactoryOverview() {
	const [actionQuery, setActionQuery] = useState('');
	const [actionType, setActionType] = useState('all');
	const [openIssueID, setOpenIssueID] = useState<string>();
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
	const allWorkflowApprovals = issues.filter((issue) => openEpics.has(issue.epicId) && issue.kind === 'approval' && ((issue.status === 'open' && issue.dispatchState === 'ready') || (issue.status === 'closed' && issue.outcome === 'failed')));
	// Stuck epics with an actionable row above are already covered; this catches the dead-ends nothing else surfaces.
	const allStuck = epics.data?.filter((epic) => epic.progress?.stuck && !allFailedWork.some((issue) => issue.epicId === epic.id) && !allBlockedWork.some((issue) => issue.epicId === epic.id) && !allMaterializations.some((issue) => issue.epicId === epic.id)) ?? [];
	// ponytail: answering live prompts stays on the session page.
	const allPrompts = [...new Map([...running.map((item) => ({ session: sessionByID.get(item.session?.id ?? ''), epic: epicByID.get(item.epicId), issueID: item.id, issueTitle: item.title })), ...planning.map(({ epic, attempt }) => ({ session: sessionByID.get(attempt.session.id), epic, issueID: attempt.workId, issueTitle: 'Planning' }))].filter((item): item is { session: Session; epic: FactoryEpic | undefined; issueID: string; issueTitle: string } => Boolean(item.session?.pendingPermission || item.session?.pendingQuestion)).map((item) => [item.session.id, item])).values()];
	const matchesAction = (type: string, ...values: Array<string | undefined>) => (actionType === 'all' || actionType === type) && fuzzyMatch(deferredActionQuery, values.join(' '));
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
	const workflowApprovals = allWorkflowApprovals.filter((issue) => matchesAction('review', epicGoal(issue.epicId), issue.id, issue.title));
	const inboxTotal = allReadyPlans.length + planGates.length + allRecoveryGates.length + allAuthorityGates.length + allProjectGates.length + allPrompts.length + allFailedWork.length + allBlockedWork.length + allMaterializations.length + allStuck.length + allWorkflowApprovals.length;
	const inboxCount = readyPlans.length + visiblePlanGates.length + recoveryGates.length + authorityGates.length + projectGates.length + prompts.length + failedWork.length + blockedWork.length + materializations.length + stuck.length + workflowApprovals.length;
	const liveStatus = (sessionID?: string) => { const session = sessionID ? sessionByID.get(sessionID) : undefined; return session && session.status !== 'done' ? <StatusBadge status={session.status} pending={session.pendingPermission || session.pendingQuestion} /> : null; };
	const openIssue = openIssueID ? issues.find((issue) => issue.id === openIssueID) : undefined;
	return <FactoryPage><OpenIssueContext.Provider value={setOpenIssueID}>
		<h2>Action inbox</h2>
		<InventoryToolbar label="Find actions" value={actionQuery} onChange={setActionQuery}><label>Action type<SelectField value={actionType} onChange={(event) => setActionType(event.target.value)}><option value="all">All actions</option><option value="planning">Planning</option><option value="review">Plan review</option><option value="project">Project scope</option><option value="recovery">Recovery</option><option value="permission">Permission</option><option value="prompt">Agent prompt</option><option value="failed">Failed work</option><option value="blocked">Blocked work</option><option value="materialization">Materialization</option><option value="stuck">Stuck epic</option></SelectField></label><span className="factory-result-count" aria-live="polite">{inboxCount} action{inboxCount === 1 ? '' : 's'}</span></InventoryToolbar>
		{epics.isLoading && <p role="status">Loading epics…</p>}
		{epics.isError && <QueryError error={epics.error} retry={() => void epics.refetch()} />}
		{issuesLoading && <p role="status">Loading action inbox…</p>}
		{issueError && <QueryError error={issueError.error} retry={() => void issueError.refetch()} />}
		{!epics.isLoading && !epics.isError && !issuesLoading && !issueError && !inboxCount && <p className="oc-empty">{inboxTotal ? 'No actions match these filters.' : 'Nothing needs your attention.'}</p>}
		{!!inboxCount && <div className="factory-list factory-list--actions factory-list--inbox" aria-label="Action inbox"><DataTableGroup label="Needs attention" noun="actions" count={inboxCount} markerClassName="factory-status-dot--blocked">
			{readyPlans.map((issue) => <PlanningItem key={issue.id} issue={issue} epic={epicByID.get(issue.epicId)} />)}
			{workflowApprovals.map((issue) => <WorkflowApprovalItem key={issue.id} issue={issue} epic={epicByID.get(issue.epicId)} />)}
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
		{openIssue && <IssueDrawer key={openIssue.id} issue={openIssue} onClose={() => setOpenIssueID(undefined)} />}
	</OpenIssueContext.Provider></FactoryPage>;
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
