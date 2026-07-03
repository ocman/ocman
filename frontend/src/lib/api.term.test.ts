import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('api.term remote routing', () => {
  it('createWindow POSTs remoteId for a remote project', async () => {
    let body = '';
    vi.stubGlobal('fetch', vi.fn((_input: RequestInfo, init?: RequestInit) => {
      body = String(init?.body || '');
      return Promise.resolve(new Response(JSON.stringify({ window: 'w' }), { status: 200 }));
    }));

    await api.term.createWindow('/repo', 'abc');

    expect(JSON.parse(body)).toEqual({ dir: '/repo', remoteId: 'abc' });
  });

  it('createWindow omits remoteId for local', async () => {
    let body = '';
    vi.stubGlobal('fetch', vi.fn((_input: RequestInfo, init?: RequestInit) => {
      body = String(init?.body || '');
      return Promise.resolve(new Response(JSON.stringify({ window: 'w' }), { status: 200 }));
    }));

    await api.term.createWindow('/repo', 'local');

    expect(JSON.parse(body)).toEqual({ dir: '/repo' });
  });

  it('killWindow POSTs remoteId for a remote project', async () => {
    let body = '';
    vi.stubGlobal('fetch', vi.fn((_input: RequestInfo, init?: RequestInit) => {
      body = String(init?.body || '');
      return Promise.resolve(new Response(null, { status: 204 }));
    }));

    await api.term.killWindow('/repo', 'ocman-abc-1', 'abc');

    expect(JSON.parse(body)).toEqual({ dir: '/repo', window: 'ocman-abc-1', remoteId: 'abc' });
  });
});
