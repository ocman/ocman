import { afterEach, describe, it, expect, vi } from 'vitest';
import { api } from './api';

function stubFetch(responder: (url: string, init?: RequestInit) => Response | Promise<Response>) {
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : (input as Request).url;
    return Promise.resolve(responder(url, init));
  }));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('api.worktree', () => {
  it('list URL-encodes the dir parameter', async () => {
    let captured = '';
    stubFetch((url) => {
      captured = url;
      return new Response(JSON.stringify({ worktrees: [] }), { status: 200 });
    });
    await api.worktree.list('/path with space');
    expect(captured).toBe('/api/worktree/list?dir=%2Fpath%20with%20space');
  });

  it('list returns parsed worktree entries', async () => {
    stubFetch(() => new Response(JSON.stringify({
      worktrees: [
        { path: '/a', branch: 'main', head: 'abc', bare: false, locked: false, main: true },
      ],
    }), { status: 200 }));
    const res = await api.worktree.list('/a');
    expect(res.worktrees).toHaveLength(1);
    expect(res.worktrees[0].branch).toBe('main');
    expect(res.worktrees[0].main).toBe(true);
  });

  it('defaultBaseRef returns the resolver string', async () => {
    stubFetch(() => new Response(JSON.stringify({ baseRef: 'main' }), { status: 200 }));
    const res = await api.worktree.defaultBaseRef('/a');
    expect(res.baseRef).toBe('main');
  });

  it('createAndLaunch POSTs JSON and returns the parsed response', async () => {
    let capturedURL = '';
    let capturedInit: RequestInit | undefined;
    stubFetch((url, init) => {
      capturedURL = url;
      capturedInit = init;
      return new Response(JSON.stringify({
        worktreePath: '/a/.worktrees/r/feature',
        branch: 'feature',
        reused: false,
        branchExisted: false,
        tmuxSession: '~/r-feature',
        opencodeLaunched: true,
      }), { status: 200 });
    });

    const res = await api.worktree.createAndLaunch({
      projectDir: '/a/r',
      branch: 'feature',
      newBranch: true,
      baseRef: 'main',
    });

    expect(capturedURL).toBe('/api/worktree/create-and-launch');
    expect(capturedInit?.method).toBe('POST');
    expect(JSON.parse(capturedInit?.body as string)).toEqual({
      projectDir: '/a/r',
      branch: 'feature',
      newBranch: true,
      baseRef: 'main',
    });
    expect(res.opencodeLaunched).toBe(true);
    expect(res.reused).toBe(false);
  });

  it('createAndLaunch surfaces server errors as Error', async () => {
    stubFetch(() => new Response('branch is already checked out elsewhere', { status: 409 }));
    await expect(
      api.worktree.createAndLaunch({
        projectDir: '/a/r',
        branch: 'foo',
        newBranch: false,
      }),
    ).rejects.toThrow(/already checked out/);
  });
});
