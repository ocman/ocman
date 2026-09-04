import { useDeferredValue, useState, type FormEvent } from 'react';
import { Link, NavLink, useNavigate, useParams } from 'react-router-dom';
import { Modal } from '../components/Modal';
import type { FactoryIssue } from '../lib/api';
import { useAddFactoryIssueComment, useFactoryGraphIssues, useFactoryIssueComments, useWorkEpics } from '../lib/queries';
import './FactoryIssues.css';

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
  const search = useDeferredValue(query.trim().toLowerCase());
  const issues = queries.flatMap((result) => result.data ?? []);
  const selected = issues.find((issue) => issue.id === issueId);
  const visible = issues.filter((issue) => `${issue.id} ${issue.title} ${issue.status}`.toLowerCase().includes(search));
  const failed = queries.find((result) => result.isError);

  return <main className="factory-issue-page">
    <nav aria-label="Factory"><NavLink to="/factory/overview">Overview</NavLink><NavLink to="/factory/epics">Epics</NavLink><NavLink to="/factory/issues">Issues</NavLink><NavLink to="/factory/queue">Queue</NavLink><NavLink to="/factory/configuration">Configuration</NavLink><NavLink to="/factory/how-to" className={({ isActive }) => `factory-how-to-link${isActive ? ' active' : ''}`}><i className="bi bi-book" aria-hidden="true" />How to</NavLink></nav>
    <h2>Factory issues</h2>
    <div className="factory-issue-toolbar" role="search"><label>Find issues<input type="search" value={query} onChange={(event) => setQuery(event.target.value)} /></label></div>
    {(epics.isLoading || queries.some((result) => result.isLoading)) && <p role="status">Loading issues...</p>}
    {epics.isError && <p role="alert">{epics.error instanceof Error ? epics.error.message : 'Factory issues are unavailable.'} <button type="button" onClick={() => void epics.refetch()}>Retry</button></p>}
    {failed && <p role="alert">{failed.error instanceof Error ? failed.error.message : 'Factory issues are unavailable.'} <button type="button" onClick={() => void failed.refetch()}>Retry</button></p>}
    {!epics.isLoading && !epics.isError && !failed && !visible.length && <p className="oc-empty">{issues.length ? 'No issues match this search.' : 'No Factory issues yet.'}</p>}
    {!!visible.length && <div className="factory-ticket-table-wrap"><table className="factory-ticket-table"><thead><tr><th>ID</th><th>Title</th><th>Status</th></tr></thead><tbody>{visible.map((issue) => <tr key={issue.id}><td>{issue.id}</td><td><button type="button" aria-label={`Open issue ${issue.id}`} onClick={() => navigate(`/factory/issues/${encodeURIComponent(issue.id)}`)}>{issue.title}</button></td><td><span>{issue.status}</span></td></tr>)}</tbody></table></div>}
    {selected && <IssueDrawer key={selected.id} issue={selected} onClose={() => navigate('/factory/issues')} />}
  </main>;
}
