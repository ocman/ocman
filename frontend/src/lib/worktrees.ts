import type { Session, WorktreeEntry } from './api';

export interface WorktreeSessionStats {
  sessions: Session[];
  lastActivity: number | null;
}

// sessionsForWorktree returns the sessions rooted at the worktree path
// plus the most recent activity timestamp among them. The association is
// purely directory equality (AD-3): a session belongs to a worktree when
// session.directory === worktree.path.
export function sessionsForWorktree(
  worktree: WorktreeEntry,
  sessions: Session[] | null,
): WorktreeSessionStats {
  const matches = (sessions ?? []).filter((s) => s.directory === worktree.path);
  let lastActivity: number | null = null;
  for (const session of matches) {
    if (lastActivity === null || session.timeUpdated > lastActivity) {
      lastActivity = session.timeUpdated;
    }
  }
  return { sessions: matches, lastActivity };
}

// tmuxWindowNameForWorktreePath mirrors the backend's
// internal/server/tmux.go:tmuxWindowNameForDirectory rule so the
// frontend can target the right worktree window in the existing project
// tmux session.
export function tmuxWindowNameForWorktreePath(path: string): string {
  const clean = path.replace(/\/+$|\/$/, '');
  const base = clean.split('/').filter(Boolean).pop() || 'wt';
  return `wt-${base}`;
}

// tmuxTargetForWorktree returns the tmux switch target for a worktree
// row. Main checkout rows target the project session itself; worktree
// rows target the deterministic `session:wt-<slug>` window name.
export function tmuxTargetForWorktree(projectSessionName: string, worktree: WorktreeEntry): string {
  if (worktree.main) return projectSessionName;
  return `${projectSessionName}:${tmuxWindowNameForWorktreePath(worktree.path)}`;
}

// projectRootForDirectory returns the repo-root path that should
// represent a session's "project" for grouping purposes. It folds
// every worktree of the same repo back to the main checkout so the
// sidebar lists e.g. `~/src/foo` once, with all of `~/src/foo`,
// `~/src/.worktrees/foo/feature-a`, and `~/src/.worktrees/foo/bug-b`
// as children — instead of three separate top-level groups.
//
// The mapping mirrors internal/worktree.PathFor:
//   <repo-parent>/.worktrees/<repo-name>/<slug>[/<sub>...]
//                         -> <repo-parent>/<repo-name>
//
// Any path that doesn't match the worktree layout falls through
// unchanged, so projects ocman doesn't manage stay self-grouping.
//
// Inputs without a recognisable structure (empty, root, malformed)
// are returned as-is.
export function projectRootForDirectory(directory: string): string {
  if (!directory) return directory;
  // Strip a trailing slash but preserve "/" itself.
  const cleaned = directory.length > 1 && directory.endsWith('/')
    ? directory.slice(0, -1)
    : directory;

  // Look for the marker segment ".worktrees" in the path. We want to
  // collapse anything of the form
  //   <prefix>/.worktrees/<repo>/<slug>...
  // back to
  //   <prefix>/<repo>
  const parts = cleaned.split('/');
  const idx = parts.indexOf('.worktrees');
  if (idx < 0) return cleaned;
  // Need at least <prefix>/.worktrees/<repo>/<slug> — i.e. two
  // components after `.worktrees`. Less than that and we can't
  // resolve the repo name confidently; return the input unchanged.
  if (parts.length < idx + 3) return cleaned;
  const prefix = parts.slice(0, idx).join('/');
  // For absolute paths the first split element is empty (the leading
  // "/"); idx === 1 with parts[0] === "" is fine. But idx === 0 (i.e.
  // a relative path that starts with ".worktrees/...") has no prefix
  // to attach the repo to, so don't fold.
  if (idx === 0) return cleaned;
  // For absolute paths, prefix is empty (`""` from the leading "/")
  // when idx === 1. That means the worktree lives directly under "/"
  // — coalescing to "/repo" would conflate it with a real "/repo"
  // project and we have no way to tell them apart. Refuse to fold.
  if (prefix === '') return cleaned;
  const repoName = parts[idx + 1];
  return `${prefix}/${repoName}`;
}
