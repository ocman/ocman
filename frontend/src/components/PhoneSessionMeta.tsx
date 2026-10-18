import type { Session } from '../lib/api';
import { relativeTime } from '../lib/format';
import { HostBadge } from './HostBadge';
import { useGitInfo } from '../lib/useGitInfo';
import { useIsPhone } from '../lib/useIsPhone';
import { projectRootForDirectory } from '../lib/worktrees';

/** Phone-only context, alongside the desktop table's unchanged columns. */
export function PhoneSessionMeta({ session }: { session: Session }) {
  const phone = useIsPhone();
  const { infos } = useGitInfo([session.directory], session.remoteId || 'local', phone);
  const branch = infos[session.directory]?.branch;
  const status = session.pendingPermission ? 'Permission needed'
    : session.pendingQuestion ? 'Question for you' : null;
  const projectRoot = projectRootForDirectory(session.directory);
  const project = projectRoot.split('/').filter(Boolean).pop() || 'Unknown project';
  const isWorktree = projectRoot !== session.directory;
  return <span className="phone-session-meta">
    <span className="phone-session-checkout">
      <HostBadge remoteId={session.remoteId} remoteName={session.remoteName} stale={session.stale} />
      <span className="phone-session-project-name" title={session.directory}>{project}</span>
      {branch && <>
        <span aria-hidden="true">·</span>
        <i className={isWorktree ? 'bi bi-diagram-2' : 'bi bi-git'} title={isWorktree ? 'Worktree' : 'Branch'} aria-hidden="true" />
        <span className="phone-session-branch" title={`Current branch: ${branch}`}>{branch}</span>
      </>}
    </span>
    {status && <span className="phone-session-pending">{status}</span>}
    <time dateTime={new Date(session.timeUpdated).toISOString()}>{relativeTime(session.timeUpdated)}</time>
  </span>;
}
