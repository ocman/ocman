import { useCallback, useMemo, useState } from 'react';
import type { Project, Session, SessionStatus } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { useProjects } from '../../lib/queries';
import { shortPath } from '../../lib/format';
import { projectRootForDirectory } from '../../lib/worktrees';
import { rollupGroupStatus } from '../../lib/sidebarHelpers';
import { remoteLog } from '../../lib/remoteLog';
import type { SidebarProjectGroup } from './SessionSidebar';

export interface UseSidebarProjectGroupsOptions {
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
  id,
  recentSessions,
  displayStatus,
}: UseSidebarProjectGroupsOptions): UseSidebarProjectGroupsResult {
  const projectOrder = useUiStore((state) => state.projectOrder);
  const setProjectOrder = useUiStore((state) => state.setProjectOrder);
  // All known projects — the sidebar "projects" view lists every
  // unarchived project, even ones with no session in the recent window.
  const projectsQuery = useProjects();
  const allProjects = projectsQuery.data;
  const archiveProject = useApiStore((state) => state.archiveProject);
  // Optimistically-archived project roots: hides the group immediately
  // while /api/projects refetches (project-archive state isn't carried
  // on the session payloads driving the sidebar).
  const [archivedProjectRoots, setArchivedProjectRoots] = useState<Set<string>>(() => new Set());

  const sidebarProjectGroups = useMemo<SidebarProjectGroup[]>(() => {
    const buckets = new Map<string, Session[]>();
    for (const s of recentSessions) {
      const key = projectRootForDirectory(s.directory || '');
      const existing = buckets.get(key);
      if (existing) existing.push(s);
      else buckets.set(key, [s]);
    }

    const effectiveStatus = (s: Session): typeof s.status =>
      s.id === id ? displayStatus : s.status;
    const rollup = (sessions: Session[]) => rollupGroupStatus(sessions, effectiveStatus);

    const groups: SidebarProjectGroup[] = Array.from(buckets.entries()).map(([directory, sessions]) => {
      const sorted = [...sessions].sort((a, b) => b.timeUpdated - a.timeUpdated);
      const remote = sorted.find((s) => s.remoteId && s.remoteId !== 'local');
      return {
        directory,
        sessions: sorted,
        lastUpdated: sorted[0]?.timeUpdated ?? 0,
        aggregate: rollup(sorted),
        remoteId: remote?.remoteId,
        remoteName: remote?.remoteName,
        platform: remote?.platform,
      };
    });

    // Drop groups for projects the user just archived (optimistic, before
    // the /api/projects refetch lands) — applies to session-bearing groups
    // too, since session payloads don't carry project-archive state.
    const visibleGroups = archivedProjectRoots.size === 0
      ? groups
      : groups.filter((g) => !archivedProjectRoots.has(g.directory));

    // Add empty groups for known unarchived projects that have no
    // session in the recent poll window, so the projects view lists
    // every active project. Archived projects (incl. server-side
    // auto-archived stale ones) stay hidden.
    for (const p of allProjects ?? []) {
      if (p.archived) continue;
      const root = projectRootForDirectory(p.directory);
      if (buckets.has(root) || archivedProjectRoots.has(root)) continue;
      buckets.set(root, []);
      visibleGroups.push({
        directory: root,
        sessions: [],
        lastUpdated: p.lastUsed,
        aggregate: rollup([]),
        remoteId: p.remoteId,
        remoteName: p.remoteName,
        platform: p.platform,
      });
    }
    // Sort project groups alphabetically by their short display path
    // (no longer by activity), then apply the user's saved manual
    // drag-and-drop order: directories present in projectOrder come
    // first (in that order); any project not yet ordered (new or never
    // dragged) keeps its alphabetical position at the end.
    visibleGroups.sort((a, b) =>
      shortPath(a.directory).localeCompare(shortPath(b.directory), undefined, {
        sensitivity: 'base',
      }),
    );
    if (projectOrder.length > 0) {
      const rank = new Map(projectOrder.map((dir, i) => [dir, i]));
      visibleGroups.sort((a, b) => {
        const ra = rank.get(a.directory);
        const rb = rank.get(b.directory);
        if (ra === undefined && rb === undefined) return 0; // keep alphabetical
        if (ra === undefined) return 1; // unordered after ordered
        if (rb === undefined) return -1;
        return ra - rb;
      });
    }

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
  }, [recentSessions, id, displayStatus, projectOrder, allProjects, archivedProjectRoots]);

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
      setArchivedProjectRoots((prev) => new Set(prev).add(root));
      archiveProject(root, true, remoteId)
        .then(() => projectsQuery.refetch())
        .catch((err) => {
          remoteLog.error('Failed to archive project', err);
          setArchivedProjectRoots((prev) => {
            const next = new Set(prev);
            next.delete(root);
            return next;
          });
        });
    },
    [archiveProject, projectsQuery],
  );

  return { allProjects, sidebarProjectGroups, handleReorderProjects, handleArchiveProjectFromSidebar };
}
