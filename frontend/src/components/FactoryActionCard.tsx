import { Link } from 'react-router-dom';
import { useState, type ReactNode } from 'react';
import type { FactoryAuthorityEscalationGate, FactoryEpic, FactoryIssue, FactoryPlanGate, FactoryRecoveryGate } from '../lib/api';
import { useClaimFactoryPlan, useDecideFactoryPlanGate, useFactoryIssues, useMaterializeFactoryPlan, usePourFactoryEpic, useReopenFactoryIssue, useResolveFactoryAuthorityGate, useResolveFactoryRecoveryGate, useWorkEpic } from '../lib/queries';
import { Button } from './Control';
import { FactoryImplementationModel } from './FactoryImplementationModel';
import { useFactoryImplementationModel } from './useFactoryImplementationModel';
import './FactoryEpicCard.css';

function PlanActions({ epic, gate }: { epic: FactoryEpic; gate: FactoryPlanGate }) {
	const epicID = epic.id;
	const implementation = useFactoryImplementationModel(epic);
  const decide = useDecideFactoryPlanGate(epicID);
  const [feedback, setFeedback] = useState('');
  return <span className="oc-factory-action-issue">
    <span>Plan revision {gate.proposalRevision}. Approval starts implementation.</span>
    <label>Plan feedback<input value={feedback} onChange={(event) => setFeedback(event.target.value)} /></label>
    <FactoryImplementationModel {...implementation} />
    <span className="oc-factory-action-buttons">{(['approve', 'revise', 'reject'] as const).map((action) => <Button key={action} type="button" disabled={decide.isPending || decide.isSuccess || (action === 'approve' && implementation.loading)} onClick={() => decide.mutate({ action, expectedRevision: gate.proposalRevision, expectedHash: gate.proposalHash, feedback, ...(action === 'approve' && implementation.model && { implementationModel: implementation.model }) })}>{action === 'approve' ? 'Approve plan' : action === 'revise' ? 'Request revision' : 'Reject plan'}</Button>)}</span>
    {decide.isPending && <span role="status">Saving decision…</span>}
    {decide.isSuccess && <span role="status">Plan decision saved.</span>}
    {decide.isError && <span role="alert">{decide.error.message}</span>}
  </span>;
}

function RecoveryActions({ gate }: { gate: FactoryRecoveryGate }) {
  const resolve = useResolveFactoryRecoveryGate();
  const [response, setResponse] = useState(gate.response ?? gate.choices?.[0] ?? '');
  const actions = gate.resolution === 'resume_pending' ? ['resume'] as const : ['resume', 'retry', 'cancel'] as const;
  return <span className="oc-factory-action-issue">
    <strong>{gate.question}</strong><span>{gate.reason}</span>
    <label>Recovery response{gate.choices?.length ? <select value={response} onChange={(event) => setResponse(event.target.value)}>{gate.choices.map((choice) => <option key={choice}>{choice}</option>)}</select> : <input value={response} onChange={(event) => setResponse(event.target.value)} />}</label>
    <span className="oc-factory-action-buttons">{actions.map((action) => <Button key={action} type="button" disabled={resolve.isPending || resolve.isSuccess} onClick={() => resolve.mutate({ id: gate.issueId, action, response: action === 'resume' ? response : '' })}>{action === 'resume' ? 'Resume work' : action === 'retry' ? 'Retry work' : 'Cancel work'}</Button>)}</span>
    {resolve.isPending && <span role="status">Saving recovery decision…</span>}
    {resolve.isSuccess && <span role="status">Recovery decision saved.</span>}
    {resolve.isError && <span role="alert">{resolve.error.message}</span>}
  </span>;
}

function AuthorityActions({ gate }: { gate: FactoryAuthorityEscalationGate }) {
  const resolve = useResolveFactoryAuthorityGate();
  const actions = gate.resolution === 'approve_pending' ? ['approve'] as const : gate.resolution === 'reject_pending' ? ['reject'] as const : ['approve', 'reject'] as const;
  return <span className="oc-factory-action-issue">
    <span>Permission: {gate.permission}</span><span>Target: {gate.target}</span>
    <span className="oc-factory-action-buttons">{actions.map((action) => <Button key={action} type="button" disabled={resolve.isPending || resolve.isSuccess} onClick={() => resolve.mutate({ id: gate.issueId, action })}>{action === 'approve' ? 'Approve once' : 'Reject permission'}</Button>)}</span>
    {resolve.isPending && <span role="status">Saving permission decision…</span>}
    {resolve.isSuccess && <span role="status">Permission decision saved.</span>}
    {resolve.isError && <span role="alert">{resolve.error.message}</span>}
  </span>;
}

function availableWorkAction(issue: FactoryIssue) {
  if (issue.status === 'closed' && ['task', 'implementation', 'delivery'].includes(issue.kind) && ['failed', 'cancelled'].includes(issue.outcome ?? '')) return 'reopen';
  if (issue.dispatchState === 'ready' && ['plan', 'materialization'].includes(issue.kind)) return issue.kind;
}

function requiresIssueAction(epic: FactoryEpic, issue: FactoryIssue, requestedAction?: string) {
  const accepts = (...actions: string[]) => !requestedAction || actions.includes(requestedAction);
  const action = availableWorkAction(issue);
  return (epic.status === 'open' && action && accepts(...WORK_REQUESTS[action])) ||
    (issue.recovery && !['resume', 'retry', 'cancel'].includes(issue.recovery.resolution) && accepts('resume_recovery', 'retry_recovery', 'cancel_recovery')) ||
    (issue.authority && !['approve', 'reject'].includes(issue.authority.resolution) && accepts('approve_authority', 'reject_authority'));
}

function requiresHumanAction(epic: FactoryEpic, issues: FactoryIssue[], issueID: string, requestedAction?: string) {
  const accepts = (...actions: string[]) => !requestedAction || actions.includes(requestedAction);
  if (epic.status === 'closed') return false;
  if (epic.planGate?.resolution === 'open' && accepts('approve_plan', 'revise_plan', 'reject_plan', 'submit_proposal')) return true;
  if (epic.status === 'open' && !issueID && !issues.length && accepts('pour')) return true;
  if (requestedAction === 'mutate_graph') return epic.status === 'open' && issues.some((issue) => (!issueID || issue.id === issueID) && issue.status === 'open');
  return issues.some((issue) => (!issueID || issue.id === issueID) && requiresIssueAction(epic, issue, requestedAction));
}

const WORK_REQUESTS: Record<string, string[]> = { reopen: ['reopen_issue', 'reopen'], plan: ['claim_plan'], materialization: ['materialize_plan'] };

function IssueDecisions({ issue }: { issue: FactoryIssue }) {
  return <>
    {issue.recovery && !['resume', 'retry', 'cancel'].includes(issue.recovery.resolution) && <RecoveryActions key={`${issue.recovery.issueId}/${issue.recovery.resolution}`} gate={issue.recovery} />}
    {issue.authority && !['approve', 'reject'].includes(issue.authority.resolution) && <AuthorityActions key={`${issue.authority.issueId}/${issue.authority.resolution}`} gate={issue.authority} />}
  </>;
}

function IssueActions({ issue, enabled }: { issue: FactoryIssue; enabled: boolean }) {
  const reopen = useReopenFactoryIssue();
  const claim = useClaimFactoryPlan(issue.epicId);
  const materialize = useMaterializeFactoryPlan();
  const mutations = [reopen, claim, materialize];
  const pending = mutations.some((mutation) => mutation.isPending);
  const error = mutations.find((mutation) => mutation.isError)?.error;
  const reopened = reopen.isSuccess;
  const target = { epicId: issue.epicId, issueId: issue.id };
  const action = enabled ? availableWorkAction(issue) : undefined;
  return <span className="oc-factory-action-issue">
    <Link to={`/factory/issues/${encodeURIComponent(issue.id)}`}>{issue.title}</Link>
    {issue.outcomeReason && <span>{issue.outcomeReason}</span>}
    <span className="oc-factory-action-buttons">
      {action === 'reopen' && !reopened && <Button type="button" disabled={pending} onClick={() => reopen.mutate(target)}>{reopen.isPending ? 'Reopening…' : 'Reopen issue'}</Button>}
      {action === 'plan' && !claim.isSuccess && <Button type="button" disabled={pending} onClick={() => claim.mutate(issue.id)}>{claim.isPending ? 'Claiming plan…' : 'Claim plan'}</Button>}
      {action === 'materialization' && !materialize.isSuccess && <Button type="button" disabled={pending} onClick={() => materialize.mutate(target)}>{materialize.isPending ? 'Materializing…' : 'Materialize plan'}</Button>}
    </span>
    {reopened && <span role="status">Issue reopened.</span>}
    {materialize.isSuccess && <span role="status">Plan materialized.</span>}
    {claim.isSuccess && <Link to={`/session/${encodeURIComponent(claim.data.session.id)}`}>Open planning session</Link>}
    <IssueDecisions issue={issue} />
    {error && <span role="alert">{error.message}</span>}
  </span>;
}

function EpicActions({ epic, issues, issueID }: { epic: FactoryEpic; issues: FactoryIssue[]; issueID: string }) {
  const pour = usePourFactoryEpic(epic.id);
  const selected = issues.filter((issue) => !issueID || issue.id === issueID);
  const gate = epic.planGate;
  return <>
    {selected.filter((issue) => requiresIssueAction(epic, issue)).map((issue) => <IssueActions key={issue.id} issue={issue} enabled={epic.status === 'open'} />)}
    {gate?.resolution === 'open' && <><Link to={`/factory/epics/${encodeURIComponent(epic.id)}`}>Review plan</Link><PlanActions key={`${gate.proposalRevision}/${gate.proposalHash}`} epic={epic} gate={gate} /></>}
    {issueID && !selected.length && <span role="status">Issue {issueID} is no longer available.</span>}
    {epic.status === 'open' && !issues.length && !issueID && <Button type="button" disabled={pour.isPending || pour.isSuccess} onClick={() => pour.mutate()}>{pour.isPending ? 'Pouring…' : 'Pour graph'}</Button>}
    {pour.isSuccess && <span role="status">Graph poured.</span>}
    {pour.isError && <span role="alert">{pour.error.message}</span>}
  </>;
}

// Inline elements only: markdown links can be children of a paragraph.
export function FactoryActionCard({ epicID, issueID = '', requestedAction, children = 'Factory actions' }: { epicID: string; issueID?: string; requestedAction?: string; children?: ReactNode }) {
  const epic = useWorkEpic(epicID);
  const issues = useFactoryIssues(epicID);
  const to = epicID ? `/factory/epics/${encodeURIComponent(epicID)}` : '/factory/overview';
  const link = <Link to={issueID ? `/factory/issues/${encodeURIComponent(issueID)}` : to}>{children}</Link>;
  if (!epicID) return link;
  if (!epic.isSuccess || !issues.isSuccess) return <>
    {(epic.isError || issues.isError) && <span role="alert">Could not load Factory actions. {link} <button type="button" onClick={() => { void epic.refetch(); void issues.refetch(); }}>Retry</button></span>}
  </>;
  if (!requiresHumanAction(epic.data, issues.data, issueID, requestedAction)) return null;
  return <span className="oc-epic-card oc-factory-action-card" aria-label="Factory human actions">
    <Link className="oc-epic-card-goal" to={to}>{epic.data?.goal || epicID || 'Factory action inbox'}</Link>
    {epic.isSuccess && issues.isSuccess && <EpicActions epic={epic.data} issues={issues.data} issueID={issueID} />}
    <span className="oc-factory-action-buttons">
      {epicID && <Link to={to}>Manage graph</Link>}
      <Link to="/factory/overview">Open action inbox</Link>
    </span>
  </span>;
}
