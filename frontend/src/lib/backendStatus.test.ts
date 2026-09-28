import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { APIError, fetchJSON } from './api';
import { markBackendReachable, markBackendUnreachable, useBackendStatus } from './backendStatus';

beforeEach(() => {
  useBackendStatus.setState({ unreachable: false, since: null, error: null });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('backendStatus store', () => {
  it('marks unreachable, keeps the first since, and clears', () => {
    markBackendUnreachable('a');
    const since = useBackendStatus.getState().since;
    expect(useBackendStatus.getState()).toMatchObject({ unreachable: true, error: 'a' });
    expect(since).not.toBeNull();
    markBackendUnreachable('b');
    expect(useBackendStatus.getState()).toMatchObject({ unreachable: true, error: 'b', since });
    markBackendReachable();
    expect(useBackendStatus.getState()).toEqual({ unreachable: false, since: null, error: null });
  });
});

describe('api.ts backend status wiring', () => {
  it('flags network failure and clears on the next success', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
    await expect(fetchJSON('/api/x')).rejects.toThrow('Backend is not responding');
    expect(useBackendStatus.getState().unreachable).toBe(true);

    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{}', { status: 200 }))));
    await fetchJSON('/api/x');
    expect(useBackendStatus.getState().unreachable).toBe(false);
  });

  it('flags a gateway 502 but not a backend 503', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('', { status: 503 }))));
    await expect(fetchJSON('/api/x')).rejects.toBeInstanceOf(APIError);
    expect(useBackendStatus.getState().unreachable).toBe(false);

    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('', { status: 502, statusText: 'Bad Gateway' }))));
    await expect(fetchJSON('/api/x')).rejects.toBeInstanceOf(APIError);
    expect(useBackendStatus.getState()).toMatchObject({ unreachable: true, error: 'HTTP 502 Bad Gateway' });
  });

  it('gives an empty-body error a status-line message', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('', { status: 502, statusText: 'Bad Gateway' }))));
    await expect(fetchJSON('/api/x')).rejects.toThrow('HTTP 502 Bad Gateway');
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('', { status: 500 }))));
    await expect(fetchJSON('/api/x')).rejects.toThrow(/^HTTP 500$/);
  });
});
