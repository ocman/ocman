import type { GitInfo } from '../lib/api';
import { Tooltip } from './Tooltip';

export function ShortPath({ path }: { path: string }) {
  const parts = (path || '').split('/').filter(Boolean);
  const last = parts.pop() || '';
  const prefix = parts.length > 0 ? parts.slice(-1).join('/') + '/' : '';
  return <><span className="short-path-prefix">{prefix}</span><span className="short-path-last">{last}</span></>;
}

function GitPendingIndicator({ info, stale }: { info: GitInfo; stale: boolean }) {
  const reasons = [];
  if (info.dirty) reasons.push('Uncommitted changes');
  if (info.ahead > 0) reasons.push(`${info.ahead} unpushed commit${info.ahead === 1 ? '' : 's'}`);
  if (!reasons.length) return null;
  const label = `${stale ? 'Last known: ' : ''}${reasons.join(' · ')}`;
  return <Tooltip content={label}>
    <span className="git-status-pending" data-stale={stale} role="img" aria-label={label} tabIndex={0}>
      {info.dirty && <i className="bi bi-pencil" aria-hidden="true" />}
      {info.ahead > 0 && <i className="bi bi-arrow-up" aria-hidden="true" />}
    </span>
  </Tooltip>;
}

export function GitStatusLine({ info, icon, pending }: {
  info?: GitInfo | null;
  icon?: 'branch' | 'worktree';
  pending?: { stale: boolean };
}) {
  if (!info || !info.branch) return null;
  const dirtyCls = info.dirty ? ' git-status-dirty' : '';
  const iconCls = icon === 'worktree' ? 'bi bi-diagram-2' : 'bi bi-git';
  return <span className={`git-status${dirtyCls}`}>
    <i className={`${iconCls} git-status-icon`} aria-hidden="true" />
    <span className="git-status-branch" title={`Current branch: ${info.branch}`}>{info.branch}</span>
    {pending ? <GitPendingIndicator info={info} stale={pending.stale} /> : info.ahead > 0 && <span className="git-status-ahead" title={`${info.ahead} commit${info.ahead === 1 ? '' : 's'} ahead of upstream (not yet pushed)`}>&uarr;{info.ahead}</span>}
    {info.behind > 0 && <span className="git-status-behind" title={`${info.behind} commit${info.behind === 1 ? '' : 's'} behind upstream (not yet pulled)`}>&darr;{info.behind}</span>}
    {!pending && info.dirty && <span className="git-status-mark" title="Working tree has uncommitted changes">*</span>}
  </span>;
}
