import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('api.tmuxLaunchOpencode', () => {
  it('POSTs remoteId when launching on a remote host', async () => {
    let body = '';
    vi.stubGlobal('fetch', vi.fn((_input: RequestInfo, init?: RequestInit) => {
      body = String(init?.body || '');
      return Promise.resolve(new Response(JSON.stringify({ session: 's' }), { status: 200 }));
    }));

    await api.tmuxLaunchOpencode('/repo', 'abc');

    expect(JSON.parse(body)).toEqual({ directory: '/repo', remoteId: 'abc' });
  });
});
