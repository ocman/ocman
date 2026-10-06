// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { APIError, fetchJSON } from './api';
import { markBackendReachable, markBackendUnreachable, useBackendStatus } from './backendStatus';

function setVisibility(state: DocumentVisibilityState): void {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state });
  document.dispatchEvent(new Event('visibilitychange'));
}

beforeEach(() => {
  markBackendReachable(); // resets the consecutive-failure count
  useBackendStatus.setState({ unreachable: false, since: null, error: null });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  // Restore jsdom's own getter.
  delete (document as { visibilityState?: unknown }).visibilityState;
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
  it('flags a second network failure in a row and clears on the next success', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
    await expect(fetchJSON('/api/x')).rejects.toThrow('Backend is not responding');
    expect(useBackendStatus.getState().unreachable).toBe(false);
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

  it('does not flag a single dropped request between successes', async () => {
    const ok = () => Promise.resolve(new Response('{}', { status: 200 }));
    const fail = () => Promise.reject(new TypeError('Load failed'));
    vi.stubGlobal('fetch', vi.fn().mockImplementationOnce(fail).mockImplementationOnce(ok).mockImplementationOnce(fail));
    await expect(fetchJSON('/api/x')).rejects.toThrow();
    await fetchJSON('/api/x');
    await expect(fetchJSON('/api/x')).rejects.toThrow();
    expect(useBackendStatus.getState().unreachable).toBe(false);
  });

  it('ignores network failures while the page is hidden', async () => {
    setVisibility('hidden');
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Load failed'))));
    await expect(fetchJSON('/api/x')).rejects.toThrow();
    await expect(fetchJSON('/api/x')).rejects.toThrow();
    expect(useBackendStatus.getState().unreachable).toBe(false);
  });

  it('ignores network failures right after the page becomes visible again', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(0); // keeps the recorded resume far in the past for later tests
    setVisibility('hidden');
    setVisibility('visible');
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Load failed'))));
    await expect(fetchJSON('/api/x')).rejects.toThrow();
    await expect(fetchJSON('/api/x')).rejects.toThrow();
    expect(useBackendStatus.getState().unreachable).toBe(false);

    vi.advanceTimersByTime(5_000);
    await expect(fetchJSON('/api/x')).rejects.toThrow();
    await expect(fetchJSON('/api/x')).rejects.toThrow();
    expect(useBackendStatus.getState().unreachable).toBe(true);
  });

  it('gives an empty-body error a status-line message', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('', { status: 502, statusText: 'Bad Gateway' }))));
    await expect(fetchJSON('/api/x')).rejects.toThrow('HTTP 502 Bad Gateway');
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('', { status: 500 }))));
    await expect(fetchJSON('/api/x')).rejects.toThrow(/^HTTP 500$/);
  });
});
