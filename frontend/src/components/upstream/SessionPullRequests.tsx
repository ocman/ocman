import { useCallback, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, ButtonGroup, SelectField, TextField, TextareaField } from '../Control';
import { Modal } from '../Modal';
import { ModalHeader } from '../ModalHeader';
import { ModalFooter } from '../ModalFooter';
import { CopyButton } from '../CopyButton';
import { prWorkflowApi, type PRLink } from '../../lib/prWorkflowApi';
import type { Upstream } from '../../lib/upstreamApi';
import { useConfirm } from '../../lib/useConfirm';
import { LinkedPRStatus } from './LinkedPRStatus';
import './SessionPullRequests.css';

interface Props { sessionId: string; platform: string; upstreams: Upstream[] }
export function SessionPullRequests(props: Props) {
  return <SessionLinks key={`${props.platform}:${props.sessionId}`} {...props} />;
}

function SessionLinks({ sessionId, platform, upstreams }: Props) {
  const [links, setLinks] = useState<PRLink[] | null>(null);
  const [remote, setRemote] = useState('');
  const [number, setNumber] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [prompt, setPrompt] = useState<string | null>(null);
  const mounted = useRef(true);
  const confirm = useConfirm();
  const load = useCallback(async () => { const result = await prWorkflowApi.links(sessionId, platform); if (mounted.current) setLinks(result.pullRequests); }, [sessionId, platform]);
  useEffect(() => {
    mounted.current = true;
    void load().catch(() => { if (mounted.current) setError('Could not load linked PRs.'); });
    const timer = window.setInterval(() => { if (document.visibilityState === 'visible') void load().catch(() => { if (mounted.current) setError('Could not refresh linked PRs.'); }); }, 30_000);
    return () => { mounted.current = false; window.clearInterval(timer); };
  }, [load]);
  const run = async (action: () => Promise<unknown>) => {
    setBusy(true); setError('');
    try { await action(); if (mounted.current) await load(); }
    catch (err) { if (mounted.current) setError(err instanceof Error ? err.message : 'PR action failed.'); }
    finally { if (mounted.current) setBusy(false); }
  };
  const change = (link: PRLink, action: string) => prWorkflowApi.change(sessionId, platform, { action, number: link.number, host: link.remote.host, repo: link.remote.repo });
  const feedback = (link: PRLink, action: string) => run(async () => { const result = await prWorkflowApi.feedback(sessionId, platform, link, action); if (mounted.current) setPrompt(result.prompt); });
  return <section className="oc-session-prs" aria-label="Linked pull requests">
    <div className="oc-session-prs-heading"><h3>Linked to this session</h3><Button variant="ghost" size="small" disabled={busy} onClick={() => void run(load)}>Refresh links</Button></div>
    {error && <p role="alert">{error} <Button variant="ghost" onClick={() => void run(load)}>Retry links</Button></p>}
    {links === null && !error && <p role="status">Loading linked PRs…</p>}
    {links?.length === 0 && <p>No linked PRs. Link one below or launch a session from a PR.</p>}
    {links?.map((link) => <article className="oc-session-pr" key={`${link.remote.host}/${link.remote.repo}/${link.number}`}>
      <a href={link.pr.url} target="_blank" rel="noreferrer noopener">#{link.number} {link.pr.title}</a>
      <p className="oc-session-pr-meta">{link.remote.host}/{link.remote.repo}</p>
      <LinkedPRStatus link={link} />
      {link.lastError && <p role="status">{link.lastError}</p>}
      <ButtonGroup label={`PR #${link.number} actions`}>
        <Button size="small" disabled={busy || (!link.watching && link.pr.status !== 'open' && link.pr.status !== 'draft')} onClick={() => void run(async () => {
          if (!link.watching && !await confirm({ title: 'Watch this PR?', description: 'Ocman will queue agent follow-ups when checks, review comments or conflicts change, even with the browser closed. This can use model credits. Existing comments are not replayed. Merging stays manual.', confirmLabel: 'Start watching' })) return;
          await change(link, link.watching ? 'unwatch' : 'watch');
        })}>{link.watching ? 'Stop watching' : 'Watch PR'}</Button>
        <Button size="small" variant="ghost" disabled={busy} onClick={() => void run(() => change(link, 'unlink'))}>Unlink</Button>
      </ButtonGroup>
      <ButtonGroup label={`PR #${link.number} feedback`}>
        <Button size="small" disabled={busy} onClick={() => void feedback(link, 'feedback')}>Address feedback</Button>
        <Button size="small" disabled={busy} onClick={() => void feedback(link, 'checks')}>Fix failing checks</Button>
        <Button size="small" disabled={busy} onClick={() => void feedback(link, 'conflicts')}>Resolve conflicts</Button>
      </ButtonGroup>
    </article>)}
    <form className="oc-session-pr-link" onSubmit={(event) => { event.preventDefault(); void run(async () => { await prWorkflowApi.change(sessionId, platform, { action: 'link', number: Number(number), remote: remote || upstreams[0]?.remote }); if (mounted.current) setNumber(''); }); }}>
      <SelectField aria-label="PR upstream" value={remote || upstreams[0]?.remote || ''} disabled={busy || !upstreams.length} onChange={(event) => setRemote(event.target.value)}>{upstreams.map((u) => <option key={u.remote} value={u.remote}>{u.remote} · {u.repo}</option>)}</SelectField>
      <TextField aria-label="PR number to link" type="number" min="1" step="1" required value={number} onChange={(event) => setNumber(event.target.value)} placeholder="PR number" disabled={busy} />
      <Button type="submit" size="small" disabled={busy || !upstreams.length || !number}>Link PR</Button>
    </form>
    <p className="oc-session-pr-meta">Watches run on the hub. No automatic merges. <Link to="/settings?tab=connections">Source-control setup</Link></p>
    {prompt !== null && <Modal label="Review feedback task" onClose={() => setPrompt(null)}><ModalHeader title="Review feedback task" closeLabel="Close feedback task" onClose={() => setPrompt(null)} /><p>Review this task before copying it into your session. Nothing has been sent.</p><TextareaField aria-label="PR feedback task" value={prompt} onChange={(event) => setPrompt(event.target.value)} rows={14} /><ModalFooter label="Feedback task actions"><CopyButton text={prompt} label="Copy task" /><Button onClick={() => setPrompt(null)}>Close</Button></ModalFooter></Modal>}
  </section>;
}
