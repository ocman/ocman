import type { ReactNode, CSSProperties } from 'react';
import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import type { GitInfo, Session } from '../../lib/api';
import { shortPath } from '../../lib/format';
import { nestSessions } from '../../lib/nestSessions';
import { compareSidebarActivity } from '../../lib/sidebarHelpers';
import { checkoutKey } from '../../lib/projectIdentity';
import { projectRootForDirectory } from '../../lib/worktrees';
import { HostBadge } from '../../components/HostBadge';
import { ProjectLabel } from '../../components/ProjectLabel';
import { GitStatusLine } from '../../components/SessionTable';
import { ArchiveIcon } from '../../components/ArchiveButton';
import type { SidebarProjectGroup as ProjectGroup } from './SessionSidebar';

export function SidebarProjectGroup({ group, collapsed, siblingGitInfos, toggleCollapsedProject,
  onNewSessionInDirectory, onArchiveProject, renderRow,
}: {
  group: ProjectGroup;
  collapsed: boolean;
  siblingGitInfos: Record<string, GitInfo>;
  toggleCollapsedProject: (directory: string) => void;
  onNewSessionInDirectory: (directory: string, remoteId?: string, platform?: string) => void;
  onArchiveProject: (directory: string, remoteId?: string) => void;
  renderRow: (session: Session, inGroup: boolean, depth: number) => ReactNode;
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: group.key ?? (group.directory || '__empty__') });
  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform), transition,
    opacity: isDragging ? 0.4 : undefined,
  };
  const remoteSession = group.sessions.find((s) => s.remoteId && s.remoteId !== 'local');
  const hostRemoteId = group.key ? group.remoteId : remoteSession?.remoteId ?? group.remoteId;
  const hostRemoteName = group.key ? group.remoteName : remoteSession?.remoteName ?? group.remoteName;
  const hostPlatform = group.key ? group.platform : group.platform ?? remoteSession?.platform ?? group.sessions[0]?.platform;
  const label = group.directory ? shortPath(group.directory) : '(unknown)';
  const byDir = new Map<string, Session[]>();
  for (const s of group.sessions) {
    const dir = checkoutKey(s.directory || group.directory, s.remoteId);
    const bucket = byDir.get(dir);
    if (bucket) bucket.push(s);
    else byDir.set(dir, [s]);
  }
  const latest = (dir: string) => Math.max(...(byDir.get(dir) ?? []).map((s) => s.timeUpdated));
  const dirs = [...byDir.keys()].sort((a, b) => {
    const aMain = byDir.get(a)?.[0]?.directory === group.directory ? 0 : 1;
    const bMain = byDir.get(b)?.[0]?.directory === group.directory ? 0 : 1;
    return aMain !== bMain ? aMain - bMain : compareSidebarActivity({ timeUpdated: latest(a) }, { timeUpdated: latest(b) });
  });
  return (
    <div ref={setNodeRef} style={style} className="session-sidebar-group">
      <div className="session-sidebar-group-header-row">
        <button type="button" className="session-sidebar-group-drag" title="Drag to reorder"
          aria-label="Drag to reorder project" {...attributes} {...listeners} onClick={(e) => e.stopPropagation()}>
          <i className="bi bi-grip-vertical" aria-hidden="true" />
        </button>
        <button type="button" className={`session-sidebar-group-header${collapsed ? ' collapsed' : ''}`}
          aria-expanded={!collapsed} title={group.directory || 'Unknown project'}
          onClick={() => toggleCollapsedProject(group.key ?? group.directory)}>
          <ProjectLabel className="session-sidebar-group-label" path={group.directory} />
        </button>
        <HostBadge remoteName={hostRemoteName} remoteId={hostRemoteId} stale={remoteSession?.stale} />
        {group.directory && <>
          <button type="button" className="session-sidebar-group-new" title={`New session in ${label}`}
            aria-label={`New session in ${label}`} onClick={(e) => {
              e.stopPropagation();
              void onNewSessionInDirectory(group.directory, hostRemoteId, hostPlatform);
            }}>+</button>
          <button type="button" className="session-sidebar-group-new" title={`Archive ${label}`}
            aria-label={`Archive ${label}`} onClick={(e) => {
              e.stopPropagation();
              onArchiveProject(group.directory, hostRemoteId);
            }}><ArchiveIcon /></button>
        </>}
      </div>
      {!collapsed && dirs.map((dirKey) => {
        const sessions = byDir.get(dirKey) ?? [];
        const checkout = sessions[0];
        const dir = checkout?.directory || group.directory;
        const isWorktree = projectRootForDirectory(dir) !== dir;
        const info = siblingGitInfos[checkoutKey(dir, checkout?.remoteId)] ?? siblingGitInfos[dir];
        const slug = dir.split('/').filter(Boolean).pop() || dir;
        const dirLabel = info?.branch ?? slug;
        return (
          <div key={dirKey} className="session-sidebar-dir-group">
            {(dirs.length > 1 || isWorktree || !!info?.branch) && (
              <div className="session-sidebar-dir-header" title={dir}>
                <span className="session-sidebar-dir-label">
                  <HostBadge remoteName={checkout?.remoteName} remoteId={checkout?.remoteId} stale={checkout?.stale} />
                  {info?.branch ? <GitStatusLine info={info} icon={isWorktree ? 'worktree' : 'branch'} /> : (
                    <span className="git-status">
                      <i className="bi bi-diagram-2 git-status-icon" aria-hidden="true" />
                      <span className="git-status-branch">{slug}</span>
                    </span>
                  )}
                </span>
                <button type="button" className="session-sidebar-group-new"
                  title={`New session on ${dirLabel}`} aria-label={`New session on ${dirLabel}`}
                  onClick={(e) => {
                    e.stopPropagation();
                    void onNewSessionInDirectory(dir, checkout?.remoteId, checkout?.platform);
                  }}>+</button>
              </div>
            )}
            {nestSessions(sessions).map(({ session, depth }) => renderRow(session, true, depth))}
          </div>
        );
      })}
    </div>
  );
}
