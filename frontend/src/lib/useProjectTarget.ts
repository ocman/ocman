import { useState } from 'react';

export interface ProjectTarget {
  directory: string | undefined;
  remoteId: string;
  projectId?: string;
}

/**
 * useProjectTarget pins the directory/owner used for upstream detection to
 * the session's project. Sibling worktrees share an OpenCode projectId (and
 * a still-loading next session has none yet), so switching between them
 * keeps the pinned target and the PR list stays put instead of reloading.
 * OpenCode's "global" project lumps unrelated non-git dirs together, so
 * those fall back to the directory itself.
 */
export function useProjectTarget(
  directory: string | undefined,
  session: { projectId?: string; remoteId?: string } | undefined,
): ProjectTarget {
  const [pinned, setPinned] = useState<ProjectTarget & { key: string }>();
  if (!session || !directory) return pinned ?? { directory, remoteId: session?.remoteId || 'local' };
  const remoteId = session.remoteId || 'local';
  const project = session.projectId && session.projectId !== 'global' ? session.projectId : `dir:${directory}`;
  const key = `${remoteId}\0${project}`;
  if (pinned?.key === key) return pinned;
  setPinned({ key, directory, remoteId, projectId: session.projectId });
  return { directory, remoteId, projectId: session.projectId };
}
