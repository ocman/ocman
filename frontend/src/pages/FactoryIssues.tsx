import { useDeferredValue, useState, type FormEvent } from 'react';
import { Link, NavLink, useNavigate, useParams } from 'react-router-dom';
import { Modal } from '../components/Modal';
import { SearchField, SelectField } from '../components/Control';
import { ProjectLabel } from '../components/ProjectLabel';
import { DataTableGroup, DataTableRow } from '../components/DataTable';
import type { FactoryEpic, FactoryIssue } from '../lib/api';
import { useAddFactoryIssueComment, useFactoryGraphIssues, useFactoryIssueComments, useWorkEpics } from '../lib/queries';
import './Factory.css';
import './FactoryIssues.css';

const issueIcons: Record<string, string> = { plan: 'bi-map', implementation: 'bi-code-square', delivery: 'bi-box-arrow-up-right', gate: 'bi-sign-stop', task: 'bi-check2-square', mol: 'bi-diagram-3', materialization: 'bi-bezier2' };

export type EpicRef = Pick<FactoryEpic, 'id' | 'goal' | 'initialProject'>;

/** Grid cell linking to the project page; renders an empty cell so grid columns stay aligned when the path is unknown. */
export function ProjectCell({ path }: { path?: string }) {
	return path ? <Link className="factory-cell" data-testid="cell-project" to={`/project/${encodeURIComponent(path)}`}><ProjectLabel path={path} /></Link> : <span className="factory-cell" data-testid="cell-project" />;
}

/** Grid cell linking to the epic; shows the goal when known, else the id. */
export function EpicCell({ id, goal }: { id?: string; goal?: string }) {
	return id ? <Link className="factory-cell" data-testid="cell-epic" to={`/factory/epics/${encodeURIComponent(id)}`} title={id}><span>{goal ?? id}</span></Link> : <span className="factory-cell" data-testid="cell-epic" />;
}

export function FactoryIssueRow({ issue, epic, onOpen }: { issue: FactoryIssue; epic?: EpicRef; onOpen: () => void }) {
	return <DataTableRow className={epic ? 'factory-grid-row' : ''} primary={<div className="factory-list-title-line"><i className={`bi ${issueIcons[issue.kind] ?? 'bi-circle'} factory-list-type-icon`} role="img" aria-label={`${issue.kind} issue`} title={`${issue.kind} issue`} /><button type="button" aria-label={`Open issue ${issue.id}`} onClick={onOpen}>{issue.title}</button></div>} secondary={<span className="factory-list-subline"><Link to={`/factory/epics/${encodeURIComponent(issue.epicId)}`}>#{issue.id}</Link>{!!issue.createdAt && <> · <time dateTime={new Date(issue.createdAt).toISOString()} title={new Date(issue.createdAt).toLocaleString()}>created {new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(issue.createdAt)}</time></>}</span>} meta={epic && <><ProjectCell path={issue.project} /><EpicCell id={epic.id} goal={epic.goal} /></>} />;
}

export function IssueDrawer({ issue, onClose }: { issue: FactoryIssue; onClose: () => void }) {
	const comments = useFactoryIssueComments(issue.epicId, issue.id);
	const addComment = useAddFactoryIssueComment(issue.epicId, issue.id);
	const [body, setBody] = useState('');
	const [status, setStatus] = useState('');
	function submit(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		if (!body.trim()) return;
		addComment.mutate(body, { onSuccess: () => { setBody(''); setStatus('Comment added.'); } });
	}
  return <Modal label={`Issue ${issue.id}`} onClose={onClose} backdropClassName="factory-issue-backdrop" dialogClassName="factory-issue-drawer">
    <header><div><span>{issue.id}</span><h2>{issue.title}</h2></div><button className="factory-issue-close" type="button" onClick={onClose} aria-label="Close issue details" title="Close"><i className="bi bi-x-lg" aria-hidden="true" /></button></header>
    {issue.description && <p>{issue.description}</p>}
    <dl>
      <div><dt>Status</dt><dd>{issue.status}</dd></div>
      <div><dt>Type</dt><dd>{issue.kind}</dd></div>
      <div><dt>Epic</dt><dd><Link to={`/factory/epics/${encodeURIComponent(issue.epicId)}`}>{issue.epicId}</Link></dd></div>
		<div><dt>Project</dt><dd><ProjectLabel path={issue.project} /></dd></div>
      {issue.parentId && <div><dt>Parent</dt><dd>{issue.parentId}</dd></div>}
      {issue.requirement && <div><dt>Requirement</dt><dd>{issue.requirement}</dd></div>}
      {issue.dispatchState && <div><dt>Dispatch</dt><dd>{issue.dispatchState}</dd></div>}
      <div><dt>Blocked by</dt><dd>{issue.blockers?.length ? issue.blockers.map((blocker, index) => <span key={blocker.id}>{index > 0 && ', '}<Link to={`/factory/issues/${encodeURIComponent(blocker.id)}`}>{blocker.id}</Link>{blocker.reason ? `: ${blocker.reason}` : ''}</span>) : 'none'}</dd></div>
		{issue.outcome && <div><dt>Outcome</dt><dd>{issue.outcome}{issue.outcomeReason ? `: ${issue.outcomeReason}` : ''}</dd></div>}
		{issue.conclusion && <div><dt>Conclusion</dt><dd>{issue.conclusion}</dd></div>}
		{issue.prUrl && <div><dt>Pull request</dt><dd><a href={issue.prUrl} target="_blank" rel="noreferrer">{issue.prUrl}</a></dd></div>}
		{issue.session?.id && <div><dt>Session</dt><dd><Link to={`/session/${encodeURIComponent(issue.session.id)}`}>{issue.session.id}</Link></dd></div>}
    </dl>
		<section className="factory-issue-comments" aria-label="Issue comments">
			<h3>Comments</h3>
			{comments.isLoading && <p role="status">Loading comments...</p>}
			{comments.isError && <p role="alert">Could not load comments.</p>}
			{comments.data && !comments.data.length && <p className="oc-empty">No comments yet.</p>}
			{!!comments.data?.length && <ol>{comments.data.map((comment) => <li key={comment.id}><header><strong>{comment.actor}</strong><time dateTime={new Date(comment.createdAt).toISOString()}>{new Date(comment.createdAt).toLocaleString()}</time></header><p>{comment.body}</p></li>)}</ol>}
			<form onSubmit={submit}><label>Add comment<textarea maxLength={16000} value={body} onChange={(event) => { setBody(event.target.value); setStatus(''); }} /></label><button type="submit" disabled={addComment.isPending || !body.trim()}>{addComment.isPending ? 'Adding...' : 'Add comment'}</button>{addComment.isError && <p role="alert">Could not add comment.</p>}{status && <p role="status">{status}</p>}</form>
		</section>
  </Modal>;
}

export function FactoryIssues() {
  const { issueId } = useParams();
  const navigate = useNavigate();
  const epics = useWorkEpics();
  const queries = useFactoryGraphIssues(epics.data);
  const [query, setQuery] = useState('');
	const [status, setStatus] = useState('active');
	const [kind, setKind] = useState('');
	const [project, setProject] = useState('');
  const search = useDeferredValue(query.trim().toLowerCase());
  const issues = queries.flatMap((result) => result.data ?? []);
  const selected = issues.find((issue) => issue.id === issueId);
	const epicByID = new Map(epics.data?.map((epic) => [epic.id, epic]));
	const kinds = [...new Set(issues.map((issue) => issue.kind))].sort();
	const projects = [...new Set(issues.map((issue) => issue.project))].sort();
	const closed = (issue: FactoryIssue) => issue.status === 'closed' || issue.status === 'completed';
	const filtered = issues.filter((issue) => `${issue.id} ${issue.title} ${issue.status} ${issue.kind} ${issue.project} ${epicByID.get(issue.epicId)?.goal ?? ''}`.toLowerCase().includes(search) && (!kind || issue.kind === kind) && (!project || issue.project === project));
	const closedCount = filtered.filter(closed).length;
	const visible = filtered.filter((issue) => status === 'all' || (status === 'closed' ? closed(issue) : !closed(issue)));
	const statusGroups: Record<string, string> = { in_progress: 'In progress', blocked: 'Blocked', retry_wait: 'Waiting', deferred: 'Waiting', open: 'Open', closed: 'Closed', completed: 'Closed' };
	const groupFor = (issue: FactoryIssue) => statusGroups[issue.status] ?? issue.status.replaceAll('_', ' ').replace(/^./, (letter) => letter.toUpperCase());
	const groupOrder = ['In progress', 'Blocked', 'Open', 'Waiting', 'Closed'];
	const groups = [...new Set(visible.map(groupFor))].sort((a, b) => groupOrder.indexOf(a) - groupOrder.indexOf(b));
  const failed = queries.find((result) => result.isError);

  return <main className="factory-issue-page">
    <nav aria-label="Factory"><NavLink to="/factory/overview">Overview</NavLink><NavLink to="/factory/epics">Epics</NavLink><NavLink to="/factory/issues">Issues</NavLink><NavLink to="/factory/queue">Queue</NavLink><NavLink to="/factory/configuration">Configuration</NavLink><NavLink to="/factory/how-to" className={({ isActive }) => `factory-how-to-link${isActive ? ' active' : ''}`}><i className="bi bi-book" aria-hidden="true" />How to</NavLink></nav>
    <h2>Factory issues</h2>
		<div className="factory-issue-toolbar factory-filter-bar" role="search"><label>Find issues<SearchField value={query} onChange={(event) => setQuery(event.target.value)} /></label><label>Issue status<SelectField value={status} onChange={(event) => setStatus(event.target.value)}><option value="active">Open only</option><option value="all">Open + closed</option><option value="closed">Closed only</option></SelectField></label><label>Issue type<SelectField value={kind} onChange={(event) => setKind(event.target.value)}><option value="">All types</option>{kinds.map((value) => <option key={value} value={value}>{value}</option>)}</SelectField></label><label>Issue project<SelectField value={project} onChange={(event) => setProject(event.target.value)}><option value="">All projects</option>{projects.map((value) => <option key={value} value={value}>{value}</option>)}</SelectField></label><span className="factory-result-count" aria-live="polite">{visible.length} shown{status === 'active' && closedCount > 0 ? ` · ${closedCount} closed hidden` : status === 'all' && closedCount > 0 ? ` · ${closedCount} closed` : ''}</span></div>
    {(epics.isLoading || queries.some((result) => result.isLoading)) && <p role="status">Loading issues...</p>}
    {epics.isError && <p role="alert">{epics.error instanceof Error ? epics.error.message : 'Factory issues are unavailable.'} <button type="button" onClick={() => void epics.refetch()}>Retry</button></p>}
    {failed && <p role="alert">{failed.error instanceof Error ? failed.error.message : 'Factory issues are unavailable.'} <button type="button" onClick={() => void failed.refetch()}>Retry</button></p>}
    {!epics.isLoading && !epics.isError && !failed && !visible.length && <p className="oc-empty">{issues.length ? 'No issues match this search.' : 'No Factory issues yet.'}</p>}
		{!!visible.length && <div className="factory-list" aria-label="Issues">{groups.map((group) => { const items = visible.filter((issue) => groupFor(issue) === group); return <DataTableGroup key={group} label={group} noun="issues" count={items.length} markerClassName={`factory-status-dot--${group.toLowerCase().replaceAll(' ', '-')}`}>{items.map((issue) => <FactoryIssueRow key={issue.id} issue={issue} epic={epicByID.get(issue.epicId)} onOpen={() => navigate(`/factory/issues/${encodeURIComponent(issue.id)}`)} />)}</DataTableGroup>; })}</div>}
    {selected && <IssueDrawer key={selected.id} issue={selected} onClose={() => navigate('/factory/issues')} />}
  </main>;
}
