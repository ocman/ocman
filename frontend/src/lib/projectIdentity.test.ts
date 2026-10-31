import { describe, expect, it } from 'vitest';
import { groupProjects, projectIdentityIndex } from './projectIdentity';
import type { Project } from './api';

describe('projectIdentityIndex', () => {
  it.each([false, true])('uses the associated project rather than a source-copy path, reversed=%s', (reversed) => {
    const projects = [
      { directory: '/private/source-2059222746', associatedDirectory: '/projects/platform-service', projectKey: 'git:platform', sessionCount: 2, messageCount: 3, totalTokensIn: 4, totalTokensOut: 5, lastUsed: 6 },
      { directory: '/projects/platform-service', projectKey: 'git:platform', sessionCount: 0, messageCount: 0, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 0 },
    ];
    if (reversed) projects.reverse();
    const identity = projectIdentityIndex(projects);
    expect(identity('/private/source-2059222746')).toMatchObject({ directory: '/projects/platform-service', key: 'git:platform' });
    expect(groupProjects(projects)).toEqual([expect.objectContaining({ directory: '/projects/platform-service', sessionCount: 2, messageCount: 3 })]);
  });
  it('keeps local project preference when an associated source copy lives on another owner', () => {
    const identity = projectIdentityIndex([
      { directory: '/private/source', associatedDirectory: '/remote/project', remoteId: 'box', projectKey: 'git:shared' },
      { directory: '/local/project', projectKey: 'git:shared' },
      { directory: '/remote/project', remoteId: 'box', projectKey: 'git:shared' },
    ]);
    expect(identity('/private/source', 'box')).toMatchObject({ directory: '/local/project', remoteId: undefined });
    expect(identity('/private/source').key).not.toBe('git:shared');
  });
  it('keeps the persisted target addressable when its remote checkout disappears', () => {
    const index = projectIdentityIndex([
      { directory: '/source', projectKey: 'merged', mergedIntoDirectory: '/target', mergedIntoRemoteId: 'box' },
    ]);
    expect(index('/target', 'box').key).toBe(index('/source').key);
  });
  it('uses the selected merge target for dashboard and session grouping', () => {
    const projects = [
      { directory: '/source', projectKey: 'target', mergedIntoDirectory: '/target', mergedIntoRemoteId: 'box', sessionCount: 2, messageCount: 3, totalTokensIn: 4, totalTokensOut: 5, lastUsed: 6 },
      { directory: '/target', remoteId: 'box', remoteName: 'Remote', projectKey: 'target', sessionCount: 10, messageCount: 20, totalTokensIn: 30, totalTokensOut: 40, lastUsed: 50 },
    ];
    const identity = projectIdentityIndex(projects);
    expect(identity('/source')).toMatchObject({ key: 'target', directory: '/target', remoteId: 'box', remoteName: 'Remote' });
    expect(identity('/target', 'box').key).toBe(identity('/source').key);
    expect(groupProjects(projects)).toEqual([expect.objectContaining({ directory: '/target', remoteId: 'box', sessionCount: 12, messageCount: 23 })]);
    expect(projects[0].directory).toBe('/source');
  });
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
