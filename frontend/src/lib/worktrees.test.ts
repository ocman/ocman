import { describe, it, expect } from 'vitest';
import {
  projectRootForDirectory,
  sessionsForWorktree,
  tmuxTargetForWorktree,
  tmuxWindowNameForWorktreePath,
} from './worktrees';
import type { Session, WorktreeEntry } from './api';

describe('sessionsForWorktree', () => {
  const wt: WorktreeEntry = {
    path: '/repo/.worktrees/feature',
    branch: 'feature',
    head: 'abc',
    bare: false,
    locked: false,
    main: false,
  };

  it('returns sessions whose directory matches the worktree path', () => {
    const sessions = [
      { id: 'a', directory: '/repo/.worktrees/feature', timeUpdated: 100 } as Session,
      { id: 'b', directory: '/repo/main', timeUpdated: 200 } as Session,
      { id: 'c', directory: '/repo/.worktrees/feature', timeUpdated: 300 } as Session,
    ];
    const stats = sessionsForWorktree(wt, sessions);
    expect(stats.sessions.map((s) => s.id)).toEqual(['a', 'c']);
    expect(stats.lastActivity).toBe(300);
  });

  it('returns empty stats when sessions are null', () => {
    const stats = sessionsForWorktree(wt, null);
    expect(stats.sessions).toEqual([]);
    expect(stats.lastActivity).toBeNull();
  });

  it('returns empty stats when there are no matching sessions', () => {
    const stats = sessionsForWorktree(wt, [
      { id: 'x', directory: '/repo/other', timeUpdated: 123 } as Session,
    ]);
    expect(stats.sessions).toEqual([]);
    expect(stats.lastActivity).toBeNull();
  });

  it('derives the deterministic worktree window name from the path basename', () => {
    expect(tmuxWindowNameForWorktreePath('/repo/.worktrees/ocman/feature-login')).toBe('wt-feature-login');
  });

  it('uses plain session target for the main checkout row', () => {
    const main = { ...wt, main: true };
    expect(tmuxTargetForWorktree('~/src/github_com/NoUseFreak/ocman', main)).toBe('~/src/github_com/NoUseFreak/ocman');
  });

  it('uses session:window target for non-main worktree rows', () => {
    expect(tmuxTargetForWorktree('~/src/github_com/NoUseFreak/ocman', wt)).toBe('~/src/github_com/NoUseFreak/ocman:wt-feature');
  });
});

describe('projectRootForDirectory', () => {
  it('returns plain non-worktree paths unchanged', () => {
    expect(projectRootForDirectory('/Users/me/src/github.com/x/repo')).toBe('/Users/me/src/github.com/x/repo');
  });

  it('strips a trailing slash but only one', () => {
    expect(projectRootForDirectory('/Users/me/src/x/repo/')).toBe('/Users/me/src/x/repo');
  });

  it('preserves the literal root path', () => {
    expect(projectRootForDirectory('/')).toBe('/');
  });

  it('returns empty input unchanged', () => {
    expect(projectRootForDirectory('')).toBe('');
  });

  it('folds a worktree path back to the main checkout', () => {
    // <prefix>/.worktrees/<repo>/<slug> -> <prefix>/<repo>
    expect(
      projectRootForDirectory('/Users/me/src/github.com/x/.worktrees/repo/feature-login'),
    ).toBe('/Users/me/src/github.com/x/repo');
  });

  it('folds a nested path inside a worktree to the main checkout', () => {
    expect(
      projectRootForDirectory('/Users/me/src/github.com/x/.worktrees/repo/feature-login/sub/dir'),
    ).toBe('/Users/me/src/github.com/x/repo');
  });

  it('leaves malformed worktree-like layouts alone (missing slug)', () => {
    // <prefix>/.worktrees/<repo>  — no slug component yet, can't
    // confidently resolve a repo. Return as-is.
    const input = '/Users/me/src/x/.worktrees/repo';
    expect(projectRootForDirectory(input)).toBe(input);
  });

  it('does not collapse a path that just happens to contain ".worktrees" at the very start', () => {
    // No prefix segments before .worktrees -> nothing to fold to.
    expect(projectRootForDirectory('/.worktrees/repo/feature')).toBe('/.worktrees/repo/feature');
  });

  it('groups sibling worktrees of the same repo to the same root', () => {
    const a = projectRootForDirectory('/Users/me/src/x/.worktrees/repo/feature-a');
    const b = projectRootForDirectory('/Users/me/src/x/.worktrees/repo/feature-b');
    const main = projectRootForDirectory('/Users/me/src/x/repo');
    expect(a).toBe(main);
    expect(b).toBe(main);
  });
});
