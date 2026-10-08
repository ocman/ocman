import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Project, Session, SessionStatus } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { useProjects } from '../../lib/queries';
import { shortPath } from '../../lib/format';
import { projectRootForDirectory } from '../../lib/worktrees';
import { projectIdentityIndex } from '../../lib/projectIdentity';
import { compareSidebarCompletion, rollupGroupStatus } from '../../lib/sidebarHelpers';
import { remoteLog } from '../../lib/remoteLog';
import type { SidebarProjectGroup } from './SessionSidebar';

export interface UseSidebarProjectGroupsOptions {
  enabled?: boolean;
  /** Active session id from the URL. */
  id: string | undefined;
  recentSessions: Session[];
  /** Display status of the active session, layered over its row. */
  displayStatus: SessionStatus;
}

export interface UseSidebarProjectGroupsResult {
  /** All known projects (unarchived + archived) from /api/projects. */
  allProjects: Project[] | undefined;
  sidebarProjectGroups: SidebarProjectGroup[];
  handleReorderProjects: (orderedDirectories: string[]) => void;
  handleArchiveProjectFromSidebar: (directory: string, remoteId?: string) => void;
}

/**
 * Sidebar project groupings: buckets recent sessions by project root,
 * adds empty groups for known unarchived projects, applies the user's
 * saved manual order, and pins pinned sessions on top. Also owns the
 * optimistic project-archive hide and the drag-and-drop reorder.
 */
export function useSidebarProjectGroups({
  enabled = true,
  id,
  recentSessions,
  displayStatus,
}: UseSidebarProjectGroupsOptions): UseSidebarProjectGroupsResult {
  const projectOrder = useUiStore((state) => state.projectOrder);
  const setProjectOrder = useUiStore((state) => state.setProjectOrder);
  const expandProjects = useUiStore((state) => state.expandProjects);
  // All known projects — the sidebar "projects" view lists every
  // unarchived project, even ones with no session in the recent window.
  const projectsQuery = useProjects({ enabled });
  const allProjects = projectsQuery.data;
  const identity = useMemo(() => projectIdentityIndex(allProjects ?? []), [allProjects]);
  const expanded = useRef('');
  useEffect(() => {
    const current = recentSessions.find(s => s.id === id);
    if (!current) return;
    const key = identity(current.directory || '', current.remoteId).key;
    const opened = `${id}:${key}`;
    if (expanded.current === opened) return;
    expanded.current = opened;
    expandProjects([key]);
  }, [id, recentSessions, identity, expandProjects]);
  const archiveProject = useApiStore((state) => state.archiveProject);
  // Optimistically-archived project roots: hides the group immediately
  // while /api/projects refetches (project-archive state isn't carried
  // on the session payloads driving the sidebar).
  const [archivedProjectRoots, setArchivedProjectRoots] = useState<Set<string>>(() => new Set());

  const sidebarProjectGroups = useMemo<SidebarProjectGroup[]>(() => {
    const buckets = new Map<string, Session[]>();
    for (const s of recentSessions) {
      const key = identity(s.directory || '', s.remoteId).key;
      const existing = buckets.get(key);
      if (existing) existing.push(s);
      else buckets.set(key, [s]);
    }

    const effectiveStatus = (s: Session): typeof s.status =>
      s.id === id ? displayStatus : s.status;
    const rollup = (sessions: Session[]) => rollupGroupStatus(sessions, effectiveStatus);

    const groups: SidebarProjectGroup[] = Array.from(buckets.entries()).map(([key, sessions]) => {
      const sorted = [...sessions].sort(compareSidebarCompletion);
      const target = identity(sorted[0].directory || '', sorted[0].remoteId);
      const representative = sorted.find((s) => (s.remoteId || 'local') === (target.remoteId || 'local'));
      return {
        key,
        directory: target.directory,
        sessions: sorted,
        lastUpdated: Math.max(...sorted.map((s) => s.timeUpdated)),
        aggregate: rollup(sorted),
        remoteId: target.remoteId || 'local',
        remoteName: target.remoteName ?? representative?.remoteName,
        platform: target.platform ?? representative?.platform,
      };
    });

    // Drop groups for projects the user just archived (optimistic, before
    // the /api/projects refetch lands) — applies to session-bearing groups
    // too, since session payloads don't carry project-archive state.
    const visibleGroups = archivedProjectRoots.size === 0
      ? groups
      : groups.filter((g) => !archivedProjectRoots.has(g.key ?? g.directory));

    // Add empty groups for known unarchived projects that have no
    // session in the recent poll window, so the projects view lists
    // every active project. Archived projects (incl. server-side
    // auto-archived stale ones) stay hidden.
    for (const p of allProjects ?? []) {
      if (p.archived) continue;
      const target = identity(p.directory, p.remoteId);
      if (buckets.has(target.key) || archivedProjectRoots.has(target.key)) continue;
      buckets.set(target.key, []);
      visibleGroups.push({
        key: target.key,
        directory: target.directory,
        sessions: [],
        lastUpdated: p.lastUsed,
        aggregate: rollup([]),
        remoteId: target.remoteId || 'local',
        remoteName: target.remoteName,
        platform: target.platform,
      });
    }
    // Sort project groups alphabetically by their short display path
    // (no longer by activity), then apply the user's saved manual
    // drag-and-drop order: directories present in projectOrder come
    // first (in that order); any project not yet ordered (new or never
    // dragged) keeps its alphabetical position at the end.
    orderSidebarProjectGroups(visibleGroups, projectOrder);

    const pinnedSessions = recentSessions
      .filter((s) => s.pinned)
      .sort((a, b) => b.pinnedAt - a.pinnedAt);
    if (pinnedSessions.length > 0) {
      visibleGroups.unshift({
        directory: '__pinned__',
        sessions: pinnedSessions,
        lastUpdated: pinnedSessions[0]?.timeUpdated ?? 0,
        aggregate: rollup(pinnedSessions),
        isPinned: true,
      });
    }

    return visibleGroups;
  }, [recentSessions, id, displayStatus, projectOrder, allProjects, archivedProjectRoots, identity]);

  // Persist a new drag-and-drop order of the (non-pinned) project
  // groups. The synthetic "__pinned__" group is excluded — it always
  // stays at the top regardless of the saved order.
  const handleReorderProjects = useCallback(
    (orderedDirectories: string[]) => {
      setProjectOrder(orderedDirectories.filter((d) => d && d !== '__pinned__'));
    },
    [setProjectOrder],
  );

  // Archive a project from the sidebar: hide its group immediately, then
  // persist + refetch /api/projects. Revert the optimistic hide on error.
  const handleArchiveProjectFromSidebar = useCallback(
    (directory: string, remoteId?: string) => {
      const root = projectRootForDirectory(directory);
      if (!root) return;
      const key = identity(root, remoteId).key;
      const members = (allProjects ?? []).filter((p) => identity(p.directory, p.remoteId).key === key);
      setArchivedProjectRoots((prev) => new Set(prev).add(key));
      Promise.all((members.length ? members : [{ directory: root, remoteId }]).map((p) =>
        archiveProject(p.directory, true, p.remoteId || 'local')))
        .then(() => projectsQuery.refetch())
        .catch((err) => {
          remoteLog.error('Failed to archive project', err);
          setArchivedProjectRoots((prev) => {
            const next = new Set(prev);
            next.delete(key);
            return next;
          });
        });
    },
    [archiveProject, projectsQuery, identity, allProjects],
  );

  return { allProjects, sidebarProjectGroups, handleReorderProjects, handleArchiveProjectFromSidebar };
}

export function orderSidebarProjectGroups(groups: SidebarProjectGroup[], projectOrder: string[]) {
  groups.sort((a, b) => shortPath(a.directory).localeCompare(shortPath(b.directory), undefined, { sensitivity: 'base' }));
  if (projectOrder.length > 0) {
    const rank = new Map(projectOrder.map((dir, i) => [dir, i]));
    groups.sort((a, b) => {
      const ra = rank.get(a.key ?? a.directory) ?? rank.get(a.directory);
      const rb = rank.get(b.key ?? b.directory) ?? rank.get(b.directory);
      if (ra === undefined && rb === undefined) return 0;
      if (ra === undefined) return 1;
      if (rb === undefined) return -1;
      return ra - rb;
    });
  }
  return groups;
}
