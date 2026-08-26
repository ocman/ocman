import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import {
  buildDirsQueryParam,
  fetchGitInfoOnce,
  _resetForTests,
} from './useGitInfo';

// We cover the React hook lifecycle in component-level tests; here we
// exercise the pure helpers it composes. They're exported so the
// branching (fetch vs skip vs error handling) is testable without
// React state/effect timing.

describe('buildDirsQueryParam', () => {
  it('returns null for empty input so callers can short-circuit the fetch', () => {
    expect(buildDirsQueryParam([])).toBeNull();
    expect(buildDirsQueryParam(undefined as unknown as string[])).toBeNull();
  });

  it('drops empty strings before encoding', () => {
    expect(buildDirsQueryParam(['', '/a', '', '/b'])).toBe('%2Fa%2C%2Fb');
  });

  it('returns null when all entries are empty (matches "" same as no input)', () => {
    expect(buildDirsQueryParam(['', '   ', ''])).toBeNull();
  });

  it('deduplicates so two sessions in the same dir do not double-fetch', () => {
    expect(buildDirsQueryParam(['/a', '/a', '/b', '/a'])).toBe('%2Fa%2C%2Fb');
  });

  it('produces a stable order so the dependency array is stable', () => {
    const a = buildDirsQueryParam(['/b', '/a', '/c']);
    const b = buildDirsQueryParam(['/a', '/c', '/b']);
    expect(a).toBe(b);
  });
});

describe('fetchGitInfoOnce', () => {
  let fetchSpy: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    _resetForTests();
    fetchSpy = vi.fn();
    globalThis.fetch = fetchSpy as unknown as typeof fetch;
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('returns an empty map without fetching when no dirs provided', async () => {
    const got = await fetchGitInfoOnce([]);
    expect(got).toEqual({});
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('fetches the canonical query and parses the response', async () => {
    fetchSpy.mockResolvedValue(
      new Response(JSON.stringify({
        '/a': { branch: 'main', ahead: 0, behind: 0, dirty: false },
        '/b': { branch: 'feat', ahead: 1, behind: 0, dirty: true },
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );

    const got = await fetchGitInfoOnce(['/a', '/b'], 'box');

    expect(got['/a'].branch).toBe('main');
    expect(got['/b'].dirty).toBe(true);
    expect(fetchSpy).toHaveBeenCalledOnce();
    const url = (fetchSpy.mock.calls[0][0] as Request).url ?? fetchSpy.mock.calls[0][0];
    expect(String(url)).toContain('/api/git/info?dirs=');
    expect(String(url)).toContain('remoteId=box');
  });

  it('throws on a non-2xx response so callers can surface the error', async () => {
    fetchSpy.mockResolvedValue(new Response('boom', { status: 500 }));
    await expect(fetchGitInfoOnce(['/a'])).rejects.toThrow();
  });

  it('returns an empty map when fetch is aborted (the caller has unmounted)', async () => {
    const ac = new AbortController();
    ac.abort();
    fetchSpy.mockImplementation((_url, init) => {
      const reason = (init as { signal?: AbortSignal })?.signal?.reason;
      return Promise.reject(reason ?? new DOMException('aborted', 'AbortError'));
    });

    const got = await fetchGitInfoOnce(['/a'], 'local', ac.signal);
    expect(got).toEqual({});
  });
});
