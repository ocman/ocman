import { describe, expect, it } from 'vitest';
import { checkoutKey, projectIdentityIndex } from './projectIdentity';

describe('projectIdentityIndex', () => {
  it('indexes final projects and folds worktrees without changing ownership', () => {
    const index = projectIdentityIndex([
      { directory: '/local/repo', projectKey: 'git:shared' },
      { directory: '/other', remoteId: 'box', remoteName: 'Box', projectKey: 'other' },
    ]);
    expect(index('/local/.worktrees/repo/topic')).toMatchObject({ key: 'git:shared', directory: '/local/repo' });
    expect(index('/other', 'box')).toMatchObject({ key: 'other', remoteName: 'Box' });
  });

  it('uses only server resolutions for source and disconnected-target directories', () => {
    const target = { key: 'final', directory: '/target', remoteId: 'box', remoteName: 'Box' };
    const index = projectIdentityIndex([{ ...target, projectKey: target.key }], [
      { checkout: { directory: '/source' }, project: target },
      { checkout: { directory: '/remote/clone', remoteId: 'other' }, project: target },
    ]);
    expect(index('/source')).toEqual(target);
    expect(index('/remote/clone', 'other')).toEqual(target);
    expect(index('/target', 'box').key).toBe('final');
    expect(index('/source', 'other').key).not.toBe('final');
  });

  it('keeps unknown same-path checkouts on different hosts separate', () => {
    const index = projectIdentityIndex([]);
    expect(index('/repo').key).not.toBe(index('/repo', 'other').key);
    expect(index('/repo', 'local').key).toBe(index('/repo').key);
    expect(checkoutKey('/repo')).toBe(checkoutKey('/repo', 'local'));
  });
});
