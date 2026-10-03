import { describe, expect, it } from 'vitest';
import { groupProjects, projectIdentityIndex } from './projectIdentity';
import type { Project } from './api';

describe('projectIdentityIndex', () => {
  it('groups checkouts and worktrees across owners using the server project key', () => {
    const index = projectIdentityIndex([
      { directory: '/local/repo', projectKey: 'git:shared' },
      { directory: '/remote/clone', remoteId: 'other', projectKey: 'git:shared' },
    ]);
    expect(index('/local/.worktrees/repo/topic').key).toBe('git:shared');
    expect(index('/remote/clone', 'other').key).toBe('git:shared');
    expect(index('/remote/clone', 'other').directory).toBe('/local/repo');
  });

  it('keeps identical paths on different hosts separate without shared remotes', () => {
    const index = projectIdentityIndex([]);
    expect(index('/repo').key).not.toBe(index('/repo', 'other').key);
    expect(index('/repo', 'local').key).toBe(index('/repo').key);
  });

  it('prefers a local checkout as the group action target regardless of input order', () => {
    const index = projectIdentityIndex([
      { directory: '/remote/repo', remoteId: 'other', projectKey: 'git:shared' },
      { directory: '/local/repo', projectKey: 'git:shared' },
    ]);
    expect(index('/remote/repo', 'other')).toMatchObject({ directory: '/local/repo', remoteId: undefined });
  });

  it.each(['local-first', 'remote-first'])('aggregates %s project rows without mutating checkout stats', (order) => {
    const projects = [
      { directory: '/a', projectKey: 'git:shared', sessionCount: 2, messageCount: 3, totalTokensIn: 4, totalTokensOut: 5, lastUsed: 6, archived: true },
      { directory: '/b', remoteId: 'other', projectKey: 'git:shared', sessionCount: 10, messageCount: 20, totalTokensIn: 30, totalTokensOut: 40, lastUsed: 50, archived: false },
    ] as Project[];
    if (order === 'remote-first') projects.reverse();
    expect(groupProjects(projects)).toEqual([expect.objectContaining({
      directory: '/a', sessionCount: 12, messageCount: 23, totalTokensIn: 34, totalTokensOut: 45, lastUsed: 50, archived: false,
    })]);
    expect(projects.find(p => p.directory === '/a')?.sessionCount).toBe(2);
    expect(projects.find(p => p.directory === '/b')?.sessionCount).toBe(10);
    expect(groupProjects([])).toEqual([]);
  });
});
