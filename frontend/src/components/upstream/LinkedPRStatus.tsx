import type { ReactNode } from 'react';
import { prWorkflowApi, type PRLink } from '../../lib/prWorkflowApi';
import { prChecksCacheKey } from '../../lib/prChecksCache';
import { CI_LABEL, usePRChecks } from '../../lib/usePRChecks';
import { Button, ButtonGroup } from '../Control';
import './ExpandableRow.css';
import './PRRow.css';

export function LinkedPRStatus({ link, busy = false, onMerge, children }: { link: PRLink; busy?: boolean; onMerge?: () => void; children?: ReactNode }) {
  const sha = link.pr.headSha ?? '';
  const checks = usePRChecks(
    prChecksCacheKey(link.remote.host, link.remote.repo, sha),
    `${link.platform}\0${link.sessionId}\0${link.remote.host}\0${link.remote.repo}\0${sha}`,
    !!sha,
    (signal, refresh) => prWorkflowApi.checks(link, signal, refresh),
  );
  const checkLabel = checks.error ? 'Could not load checks' : checks.loading && !checks.loaded ? 'Loading checks…' : checks.state === 'failure' ? 'Checks failed' : CI_LABEL[checks.state];
  return <><div className="oc-session-pr-statuses" aria-label={`PR #${link.number} status`}>
    <span className={`oc-upstream-status oc-upstream-status-${link.pr.status}`}>{link.pr.status}</span>
    <span className="oc-session-pr-checks"><span className={`oc-upstream-ci-dot oc-upstream-ci-dot-${checks.error ? 'unknown' : checks.state}`} role="img" aria-label={checkLabel} />{checkLabel}</span>
    <span className={`oc-session-pr-watch${link.watching ? ' watching' : ''}`}>{link.watching ? 'Watching' : 'Not watching'}</span>
  </div>
  <ButtonGroup label={`PR #${link.number} merge and fix`} className="oc-session-pr-feedback">
    {onMerge && link.pr.status === 'open' && link.pr.mergeable === true && !!sha && checks.loaded && !checks.error && checks.state === 'success' && <Button size="small" variant="accent" disabled={busy} onClick={onMerge}>Merge</Button>}
    {children}
  </ButtonGroup></>;
}
