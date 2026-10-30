import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, ButtonGroup, TextareaField } from '../Control';
import { Modal } from '../Modal';
import { ModalHeader } from '../ModalHeader';
import { ModalFooter } from '../ModalFooter';
import { CopyButton } from '../CopyButton';
import { IconButton } from '../IconButton';
import { Tooltip } from '../Tooltip';
import { prWorkflowApi, type PRLink } from '../../lib/prWorkflowApi';
import { useConfirm } from '../../lib/useConfirm';
import { LinkedPRStatus } from './LinkedPRStatus';
import './SessionPullRequests.css';

interface Props { sessionId: string; platform: string; linksVersion?: number }
export function SessionPullRequests(props: Props) {
  return <SessionLinks key={`${props.platform}:${props.sessionId}`} {...props} />;
}

function SessionLinks({ sessionId, platform, linksVersion }: Props) {
  const [links, setLinks] = useState<PRLink[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [prompt, setPrompt] = useState<string | null>(null);
  const mounted = useRef(true);
  const confirm = useConfirm();
  const load = useCallback(async (refresh = false) => { const result = await prWorkflowApi.links(sessionId, platform, refresh); if (mounted.current) { setLinks(result.pullRequests); setError(''); } }, [sessionId, platform]);
  useEffect(() => {
    mounted.current = true;
    void load().catch(() => { if (mounted.current) setError('Could not load linked PRs.'); });
    const timer = window.setInterval(() => { if (document.visibilityState === 'visible') void load().catch(() => { if (mounted.current) setError('Could not refresh linked PRs.'); }); }, 60_000);
    return () => { mounted.current = false; window.clearInterval(timer); };
  }, [load, linksVersion]);
  const run = async (action: () => Promise<unknown>, reload = true) => {
    setBusy(true); setError('');
    try { await action(); if (mounted.current && reload) await load(); }
    catch (err) { if (mounted.current) setError(err instanceof Error ? err.message : 'PR action failed.'); }
    finally { if (mounted.current) setBusy(false); }
  };
  const change = (link: PRLink, action: string) => prWorkflowApi.change(sessionId, platform, { action, number: link.number, host: link.remote.host, repo: link.remote.repo });
  const feedback = (link: PRLink, action: string) => run(async () => { const result = await prWorkflowApi.feedback(sessionId, platform, link, action); if (mounted.current) setPrompt(result.prompt); });
  if (links?.length === 0 && !error) return null;
  return <section className="oc-session-prs" aria-label="Linked pull requests">
    <div className="oc-session-prs-heading">
      <div className="oc-session-prs-title"><h3>Linked to this session</h3>
        <Tooltip content="Watches run on the hub. No automatic merges. Configure source control in Settings → Connections.">
          <IconButton variant="ghost" icon="bi-question-circle" label="About linked pull requests" title="" />
        </Tooltip>
      </div>
      <Button variant="ghost" size="small" disabled={busy} onClick={() => void run(() => load(true), false)}>Refresh links</Button>
    </div>
    {error && <p role="alert">{error} <Button variant="ghost" onClick={() => void run(load, false)}>Retry links</Button></p>}
    {links === null && !error && <p role="status">Loading linked PRs…</p>}
    {links?.map((link) => <article className="oc-session-pr" key={`${link.remote.host}/${link.remote.repo}/${link.number}`}>
      <a href={link.pr.url} target="_blank" rel="noreferrer noopener">#{link.number} {link.pr.title}</a>
      <div className="oc-session-pr-repo">
      <p className="oc-session-pr-meta">{link.remote.host}/{link.remote.repo}</p>
      <ButtonGroup label={`PR #${link.number} actions`}>
        <IconButton variant="ghost" icon={link.watching ? 'bi-sign-stop' : 'bi-eye'} label={link.watching ? 'Stop watching' : 'Watch PR'} disabled={busy || (!link.watching && link.pr.status !== 'open' && link.pr.status !== 'draft')} onClick={() => void run(async () => {
          if (!link.watching && !await confirm({ title: 'Watch this PR?', description: 'Ocman will queue agent follow-ups when checks, review comments or conflicts change, even with the browser closed. This can use model credits. Existing comments are not replayed. Merging stays manual.', confirmLabel: 'Start watching' })) return;
          await change(link, link.watching ? 'unwatch' : 'watch');
        })} />
        <IconButton variant="ghost" icon="bi-x-lg" label="Unlink" disabled={busy} onClick={() => void run(() => change(link, 'unlink'))} />
      </ButtonGroup>
      </div>
      <LinkedPRStatus link={link} />
      {link.lastError && <p role="status">{link.lastError}</p>}
      <ButtonGroup label={`PR #${link.number} feedback`}>
        <Button size="small" disabled={busy} onClick={() => void feedback(link, 'feedback')}>Address feedback</Button>
        <Button size="small" disabled={busy} onClick={() => void feedback(link, 'checks')}>Fix failing checks</Button>
        <Button size="small" disabled={busy} onClick={() => void feedback(link, 'conflicts')}>Resolve conflicts</Button>
      </ButtonGroup>
    </article>)}
    {prompt !== null && <Modal label="Review feedback task" onClose={() => setPrompt(null)}><ModalHeader title="Review feedback task" closeLabel="Close feedback task" onClose={() => setPrompt(null)} /><p>Review this task before copying it into your session. Nothing has been sent.</p><TextareaField aria-label="PR feedback task" value={prompt} onChange={(event) => setPrompt(event.target.value)} rows={14} /><ModalFooter label="Feedback task actions"><CopyButton text={prompt} label="Copy task" /><Button onClick={() => setPrompt(null)}>Close</Button></ModalFooter></Modal>}
  </section>;
}
