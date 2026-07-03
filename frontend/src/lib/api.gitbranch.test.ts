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

describe('api.gitBranches / gitCheckout', () => {
  it('gitBranches URL-encodes the dir and parses branches', async () => {
    let captured = '';
    stubFetch((url) => {
      captured = url;
      return new Response(JSON.stringify({ branches: ['main', 'feature/x'] }), { status: 200 });
    });
    const res = await api.gitBranches('/path with space');
    expect(captured).toBe('/api/git/branches?dir=%2Fpath%20with%20space');
    expect(res.branches).toEqual(['main', 'feature/x']);
  });

  it('gitCheckout POSTs dir + branch', async () => {
    let capturedURL = '';
    let capturedBody: unknown = null;
    stubFetch((url, init) => {
      capturedURL = url;
      capturedBody = JSON.parse((init?.body as string) ?? '{}');
      return new Response(JSON.stringify({ branch: 'feature/x' }), { status: 200 });
    });
    const res = await api.gitCheckout('/a', 'feature/x');
    expect(capturedURL).toBe('/api/git/checkout');
    expect(capturedBody).toEqual({ dir: '/a', branch: 'feature/x' });
    expect(res.branch).toBe('feature/x');
  });

  it('gitCheckout surfaces a 409 dirty-tree conflict as an Error', async () => {
    stubFetch(() => new Response('checkout would overwrite local changes', { status: 409 }));
    await expect(api.gitCheckout('/a', 'other')).rejects.toThrow(/overwrite/);
  });
});
