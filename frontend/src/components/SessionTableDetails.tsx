import type { GitInfo, Session } from '../lib/api';

export function UnreadBadge({ session }: { session: Session }) {
  if (!session.unreadCount || session.seen) return null;
  const label = session.unreadCount > 99 ? '99+' : String(session.unreadCount);
  return <span className="session-unread-badge" data-testid="session-unread-badge"
    title={`${session.unreadCount} new message${session.unreadCount === 1 ? '' : 's'} since you last viewed this session`}>{label}</span>;
}

export function ShortPath({ path }: { path: string }) {
  const parts = (path || '').split('/').filter(Boolean);
  const last = parts.pop() || '';
  const prefix = parts.length > 0 ? parts.slice(-1).join('/') + '/' : '';
  return <><span className="short-path-prefix">{prefix}</span><span className="short-path-last">{last}</span></>;
}

export function GitStatusLine({ info, icon }: { info?: GitInfo | null; icon?: 'branch' | 'worktree' }) {
  if (!info || !info.branch) return null;
  const dirtyCls = info.dirty ? ' git-status-dirty' : '';
  const iconCls = icon === 'worktree' ? 'bi bi-diagram-2' : 'bi bi-git';
  return <span className={`git-status${dirtyCls}`}>
    <i className={`${iconCls} git-status-icon`} aria-hidden="true" />
    <span className="git-status-branch" title={`Current branch: ${info.branch}`}>{info.branch}</span>
    {info.ahead > 0 && <span className="git-status-ahead" title={`${info.ahead} commit${info.ahead === 1 ? '' : 's'} ahead of upstream (not yet pushed)`}>&uarr;{info.ahead}</span>}
    {info.behind > 0 && <span className="git-status-behind" title={`${info.behind} commit${info.behind === 1 ? '' : 's'} behind upstream (not yet pulled)`}>&darr;{info.behind}</span>}
    {info.dirty && <span className="git-status-mark" title="Working tree has uncommitted changes">*</span>}
  </span>;
}
