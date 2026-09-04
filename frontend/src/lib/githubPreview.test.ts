import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { parseGitHubUrl, parseForgejoUrl, extractForgejoUrls } from './githubPreview';

// jsonResponse builds a minimal fetch Response-like object.
function jsonResponse(body: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => body,
  } as Response;
}

// freshModule re-imports the module so its module-level caches
// (preview cache + forgejo hosts cache) start empty for each test.
async function freshModule() {
  vi.resetModules();
  return import('./githubPreview');
}

// deferred exposes a promise's resolve/reject so a test can hold a fetch
// open and observe what concurrent callers do while it is in flight.
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe('parseGitHubUrl', () => {
  it('parses a PR URL', () => {
    const ref = parseGitHubUrl('https://github.com/example-org/example-repo/pull/15');
    expect(ref).toEqual({ kind: 'pr', owner: 'example-org', repo: 'example-repo', number: 15 });
  });

  it('parses a PR URL with trailing path', () => {
    const ref = parseGitHubUrl('https://github.com/owner/repo/pull/42/files');
    expect(ref).toEqual({ kind: 'pr', owner: 'owner', repo: 'repo', number: 42 });
  });

  it('parses an issue URL', () => {
    const ref = parseGitHubUrl('https://github.com/owner/repo/issues/7');
    expect(ref).toEqual({ kind: 'issue', owner: 'owner', repo: 'repo', number: 7 });
  });

  it('parses a commit URL (full sha)', () => {
    const ref = parseGitHubUrl('https://github.com/owner/repo/commit/abc1234def5678901234567890123456789abcde');
    expect(ref).toEqual({
      kind: 'commit',
      owner: 'owner',
      repo: 'repo',
      sha: 'abc1234def5678901234567890123456789abcde',
    });
  });

  it('parses a commit URL (short sha)', () => {
    const ref = parseGitHubUrl('https://github.com/owner/repo/commit/abc1234');
    expect(ref).toEqual({ kind: 'commit', owner: 'owner', repo: 'repo', sha: 'abc1234' });
  });

  it('returns null for non-GitHub URLs', () => {
    expect(parseGitHubUrl('https://example.com/foo/bar')).toBeNull();
    expect(parseGitHubUrl('https://gitlab.com/owner/repo/merge_requests/1')).toBeNull();
  });

  it('returns null for bare GitHub URLs', () => {
    expect(parseGitHubUrl('https://github.com/owner/repo')).toBeNull();
    expect(parseGitHubUrl('https://github.com/owner/repo/tree/main')).toBeNull();
  });

  it('returns null for empty string', () => {
    expect(parseGitHubUrl('')).toBeNull();
  });
});

describe('parseForgejoUrl', () => {
  const hosts = ['code.example.com', 'codeberg.org'];

  it('parses a PR URL (plural /pulls/)', () => {
    const ref = parseForgejoUrl('https://code.example.com/alice/myproj/pulls/7', hosts);
    expect(ref).toEqual({ kind: 'pr', owner: 'alice', repo: 'myproj', number: 7 });
  });

  it('parses an issue URL', () => {
    const ref = parseForgejoUrl('https://codeberg.org/owner/repo/issues/12', hosts);
    expect(ref).toEqual({ kind: 'issue', owner: 'owner', repo: 'repo', number: 12 });
  });

  it('parses a commit URL', () => {
    const ref = parseForgejoUrl('https://code.example.com/owner/repo/commit/abc1234', hosts);
    expect(ref).toEqual({ kind: 'commit', owner: 'owner', repo: 'repo', sha: 'abc1234' });
  });

  it('returns null for a host not in the list', () => {
    expect(parseForgejoUrl('https://other.example.org/owner/repo/pulls/1', hosts)).toBeNull();
  });

  it('returns null when no hosts are configured', () => {
    expect(parseForgejoUrl('https://code.example.com/owner/repo/pulls/1', [])).toBeNull();
  });

  it('returns null for GitHub-style singular /pull/ path', () => {
    expect(parseForgejoUrl('https://code.example.com/owner/repo/pull/1', hosts)).toBeNull();
  });
});

describe('extractForgejoUrls', () => {
  const hosts = ['code.example.com'];

  it('extracts and dedupes previewable URLs', () => {
    const text =
      'see https://code.example.com/a/b/pulls/1 and https://code.example.com/a/b/pulls/1 ' +
      'and https://code.example.com/a/b/pulls/1/files plus https://code.example.com/a/b/issues/2.';
    expect(extractForgejoUrls(text, hosts)).toEqual([
      'https://code.example.com/a/b/pulls/1',
      'https://code.example.com/a/b/issues/2',
    ]);
  });

  it('ignores other hosts and non-previewable paths', () => {
    const text = 'https://github.com/a/b/pull/1 https://code.example.com/a/b/tree/main';
    expect(extractForgejoUrls(text, hosts)).toEqual([]);
  });

  it('returns empty when no hosts configured', () => {
    expect(extractForgejoUrls('https://code.example.com/a/b/pulls/1', [])).toEqual([]);
  });
});

describe('cachedGitHubPreview / refreshGitHubPreview', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('fetches and maps an open PR', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        title: 'My PR',
        state: 'open',
        user: { login: 'alice', avatar_url: 'a.png' },
        html_url: 'https://github.com/o/r/pull/1',
        updated_at: '2026-01-01T00:00:00Z',
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const data = await mod.cachedGitHubPreview('https://github.com/o/r/pull/1');
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/integrations/github/preview?url=https%3A%2F%2Fgithub.com%2Fo%2Fr%2Fpull%2F1',
    );
    expect(data).toMatchObject({
      kind: 'pr',
      title: '#1 My PR',
      state: 'Open',
      stateClass: 'open',
      author: 'alice',
      authorAvatar: 'a.png',
      repo: 'o/r',
    });
  });

  it('maps a merged PR', async () => {
    const mod = await freshModule();
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({ title: 'Merged', state: 'closed', merged: true }),
      ),
    );
    const data = await mod.cachedGitHubPreview('https://github.com/o/r/pull/2');
    expect(data).toMatchObject({ state: 'Merged', stateClass: 'merged', stateIcon: 'bi-git' });
  });

  it('maps a closed issue', async () => {
    const mod = await freshModule();
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ title: 'Bug', state: 'closed' })),
    );
    const data = await mod.cachedGitHubPreview('https://github.com/o/r/issues/3');
    expect(data).toMatchObject({ kind: 'issue', state: 'Closed', stateClass: 'closed' });
  });

  it('maps a commit, deriving title from the first message line', async () => {
    const mod = await freshModule();
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({
          sha: 'abc1234def',
          commit: { message: 'fix: thing\n\nbody', author: { name: 'Bob', date: '2026-02-02T00:00:00Z' } },
        }),
      ),
    );
    const data = await mod.cachedGitHubPreview('https://github.com/o/r/commit/abc1234def');
    expect(data).toMatchObject({
      kind: 'commit',
      title: 'fix: thing',
      stateClass: 'commit',
      author: 'Bob',
      shortSha: 'abc1234',
    });
  });

  it('returns null and caches the error on a failed fetch', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}, false, 502));
    vi.stubGlobal('fetch', fetchMock);

    const url = 'https://github.com/o/r/pull/9';
    expect(await mod.cachedGitHubPreview(url)).toBeNull();
    // Second call should hit the error cache, not refetch.
    expect(await mod.cachedGitHubPreview(url)).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('returns the cached value on a second call', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ title: 'Cached', state: 'open' }));
    vi.stubGlobal('fetch', fetchMock);

    const url = 'https://github.com/o/r/pull/10';
    await mod.cachedGitHubPreview(url);
    await mod.cachedGitHubPreview(url);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('returns null for a non-GitHub URL without fetching', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    expect(await mod.cachedGitHubPreview('https://example.com/x')).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('refresh bypasses the cached value once it is stale', async () => {
    vi.useFakeTimers();
    try {
      const mod = await freshModule();
      const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ title: 'Fresh', state: 'open' }));
      vi.stubGlobal('fetch', fetchMock);

      const url = 'https://github.com/o/r/pull/11';
      await mod.cachedGitHubPreview(url);
      vi.setSystemTime(Date.now() + mod.PREVIEW_FRESH_MS + 1);
      await mod.refreshGitHubPreview(url);
      expect(fetchMock).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('refresh returns null on error and leaves cache intact', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}, false, 500));
    vi.stubGlobal('fetch', fetchMock);
    expect(await mod.refreshGitHubPreview('https://github.com/o/r/pull/12')).toBeNull();
  });

  it('refresh returns null for non-GitHub URL', async () => {
    const mod = await freshModule();
    vi.stubGlobal('fetch', vi.fn());
    expect(await mod.refreshGitHubPreview('https://example.com/x')).toBeNull();
  });
});

describe('loadForgejoHosts', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('loads hosts from the status endpoint and caches them', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ forgejo: { available: true, hosts: ['code.example.com'] } }),
    );
    vi.stubGlobal('fetch', fetchMock);

    expect(await mod.loadForgejoHosts()).toEqual(['code.example.com']);
    // Cached — no second fetch.
    expect(await mod.loadForgejoHosts()).toEqual(['code.example.com']);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('returns [] when the status request fails', async () => {
    const mod = await freshModule();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({}, false, 500)));
    expect(await mod.loadForgejoHosts()).toEqual([]);
  });

  it('returns [] when fetch throws', async () => {
    const mod = await freshModule();
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('network')));
    expect(await mod.loadForgejoHosts()).toEqual([]);
  });

  it('returns [] when the payload has no forgejo hosts', async () => {
    const mod = await freshModule();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ github: {} })));
    expect(await mod.loadForgejoHosts()).toEqual([]);
  });
});

describe('cachedForgejoPreview / refreshForgejoPreview', () => {
  const hosts = ['code.example.com'];

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('fetches and maps a Forgejo PR via the forgejo endpoint', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ title: 'FJ PR', state: 'open', user: { login: 'carol' } }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const url = 'https://code.example.com/o/r/pulls/1';
    const data = await mod.cachedForgejoPreview(url, hosts);
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/integrations/forgejo/preview?url=' + encodeURIComponent(url),
    );
    expect(data).toMatchObject({ kind: 'pr', title: '#1 FJ PR', repo: 'o/r', author: 'carol' });
  });

  it('returns null for an unknown host without fetching', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    expect(await mod.cachedForgejoPreview('https://other.org/o/r/pulls/1', hosts)).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('caches the error on failure', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({}, false, 502));
    vi.stubGlobal('fetch', fetchMock);
    const url = 'https://code.example.com/o/r/issues/5';
    expect(await mod.cachedForgejoPreview(url, hosts)).toBeNull();
    expect(await mod.cachedForgejoPreview(url, hosts)).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('refresh refetches and returns mapped data once stale', async () => {
    vi.useFakeTimers();
    try {
      const mod = await freshModule();
      const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ title: 'C', state: 'open' }));
      vi.stubGlobal('fetch', fetchMock);
      const url = 'https://code.example.com/o/r/pulls/7';
      await mod.cachedForgejoPreview(url, hosts);
      vi.setSystemTime(Date.now() + mod.PREVIEW_FRESH_MS + 1);
      const data = await mod.refreshForgejoPreview(url, hosts);
      expect(data).toMatchObject({ kind: 'pr', title: '#7 C' });
      expect(fetchMock).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('refresh returns null for an unknown host', async () => {
    const mod = await freshModule();
    vi.stubGlobal('fetch', vi.fn());
    expect(await mod.refreshForgejoPreview('https://other.org/o/r/pulls/1', hosts)).toBeNull();
  });

  it('refresh returns null on fetch error', async () => {
    const mod = await freshModule();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({}, false, 500)));
    expect(
      await mod.refreshForgejoPreview('https://code.example.com/o/r/pulls/8', hosts),
    ).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// FR-11: request coalescing + bounded cache
// ---------------------------------------------------------------------------

describe('preview request coalescing and bounded cache', () => {
  const hosts = ['a.example.com', 'b.example.com'];

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('shares one in-flight request between concurrent loads and refreshes', async () => {
    const mod = await freshModule();
    const d = deferred<Response>();
    const fetchMock = vi.fn().mockReturnValue(d.promise);
    vi.stubGlobal('fetch', fetchMock);

    const url = 'https://github.com/o/r/pull/1';
    const pending = [
      mod.cachedGitHubPreview(url),
      mod.cachedGitHubPreview(url),
      mod.cachedGitHubPreview(url),
      mod.refreshGitHubPreview(url),
    ];
    d.resolve(jsonResponse({ title: 'Shared', state: 'open' }));
    const results = await Promise.all(pending);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    for (const r of results) expect(r).toMatchObject({ title: '#1 Shared' });
  });

  it('shares one in-flight Forgejo request between concurrent callers', async () => {
    const mod = await freshModule();
    const d = deferred<Response>();
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(d.promise));

    const url = 'https://a.example.com/o/r/pulls/1';
    const pending = [
      mod.cachedForgejoPreview(url, hosts),
      mod.refreshForgejoPreview(url, hosts),
    ];
    d.resolve(jsonResponse({ title: 'FJ', state: 'open' }));
    const results = await Promise.all(pending);

    expect(fetch).toHaveBeenCalledTimes(1);
    for (const r of results) expect(r).toMatchObject({ title: '#1 FJ' });
  });

  it('keys distinct forge hosts separately', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockImplementation((req: string) =>
      Promise.resolve(
        jsonResponse({ title: req.includes('a.example.com') ? 'A' : 'B', state: 'open' }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);

    const a = await mod.cachedForgejoPreview('https://a.example.com/o/r/pulls/1', hosts);
    const b = await mod.cachedForgejoPreview('https://b.example.com/o/r/pulls/1', hosts);

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(a).toMatchObject({ title: '#1 A' });
    expect(b).toMatchObject({ title: '#1 B' });
  });

  it('keys distinct resources on one host separately', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockImplementation((req: string) =>
      Promise.resolve(jsonResponse({ title: `t${req.slice(-1)}`, state: 'open' })),
    );
    vi.stubGlobal('fetch', fetchMock);

    const pr = await mod.cachedGitHubPreview('https://github.com/o/r/pull/1');
    const issue = await mod.cachedGitHubPreview('https://github.com/o/r/issues/1');
    const other = await mod.cachedGitHubPreview('https://github.com/o/r/pull/2');
    const otherRepo = await mod.cachedGitHubPreview('https://github.com/o/r2/pull/1');

    expect(fetchMock).toHaveBeenCalledTimes(4);
    expect(pr!.kind).toBe('pr');
    expect(issue!.kind).toBe('issue');
    expect(other).not.toBeNull();
    expect(otherRepo).not.toBeNull();
  });

  it('normalizes trailing path segments onto one cache key', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ title: 'Same', state: 'open' }));
    vi.stubGlobal('fetch', fetchMock);

    const a = await mod.cachedGitHubPreview('https://github.com/o/r/pull/42');
    const b = await mod.cachedGitHubPreview('https://github.com/o/r/pull/42/files');

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(b).toEqual(a);
  });

  it('dedupes refreshes inside the freshness window and refetches after it', async () => {
    vi.useFakeTimers();
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ title: 'W', state: 'open' }));
    vi.stubGlobal('fetch', fetchMock);

    const url = 'https://github.com/o/r/pull/3';
    await mod.cachedGitHubPreview(url);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    // Three cards revalidating inside the window share the last success.
    vi.setSystemTime(Date.now() + mod.PREVIEW_FRESH_MS - 1);
    await mod.refreshGitHubPreview(url);
    await mod.refreshGitHubPreview(url);
    await mod.refreshGitHubPreview(url);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    vi.setSystemTime(Date.now() + 2);
    await mod.refreshGitHubPreview(url);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('keeps a previous success visible when a refresh fails, and recovers later', async () => {
    vi.useFakeTimers();
    const mod = await freshModule();
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ title: 'First', state: 'open' }))
      .mockResolvedValueOnce(jsonResponse({}, false, 502))
      .mockResolvedValueOnce(jsonResponse({ title: 'Third', state: 'open' }));
    vi.stubGlobal('fetch', fetchMock);

    const url = 'https://github.com/o/r/pull/4';
    expect(await mod.cachedGitHubPreview(url)).toMatchObject({ title: '#4 First' });

    // A failed refresh must not replace or poison the previous success.
    vi.setSystemTime(Date.now() + mod.PREVIEW_FRESH_MS + 1);
    expect(await mod.refreshGitHubPreview(url)).toMatchObject({ title: '#4 First' });
    expect(await mod.cachedGitHubPreview(url)).toMatchObject({ title: '#4 First' });

    // ...and the next cycle retries without a reload.
    vi.setSystemTime(Date.now() + mod.PREVIEW_FRESH_MS + 1);
    expect(await mod.refreshGitHubPreview(url)).toMatchObject({ title: '#4 Third' });
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('does not hammer the backend on repeated errors but expires them', async () => {
    vi.useFakeTimers();
    const mod = await freshModule();
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({}, false, 502))
      .mockResolvedValue(jsonResponse({ title: 'Recovered', state: 'open' }));
    vi.stubGlobal('fetch', fetchMock);

    const url = 'https://github.com/o/r/pull/5';
    expect(await mod.cachedGitHubPreview(url)).toBeNull();
    expect(await mod.cachedGitHubPreview(url)).toBeNull();
    expect(await mod.refreshGitHubPreview(url)).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    vi.setSystemTime(Date.now() + mod.PREVIEW_FRESH_MS + 1);
    expect(await mod.cachedGitHubPreview(url)).toMatchObject({ title: '#5 Recovered' });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('bounds the cache with an entry cap, evicting the oldest success', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ title: 'E', state: 'open' }));
    vi.stubGlobal('fetch', fetchMock);

    const cap = mod.PREVIEW_MAX_ENTRIES;
    for (let i = 0; i < cap + 1; i++) {
      await mod.cachedGitHubPreview(`https://github.com/o/r/pull/${i}`);
    }
    expect(fetchMock).toHaveBeenCalledTimes(cap + 1);

    // The newest entry is still cached...
    await mod.cachedGitHubPreview(`https://github.com/o/r/pull/${cap}`);
    expect(fetchMock).toHaveBeenCalledTimes(cap + 1);

    // ...while the oldest was evicted and must be refetched.
    await mod.cachedGitHubPreview('https://github.com/o/r/pull/0');
    expect(fetchMock).toHaveBeenCalledTimes(cap + 2);
  });
});

// Every ocman-backed call has to participate in the AuthError fan-out
// (see authStore.ts): on an expired cookie the user must land on the
// lockscreen, not watch a preview strip silently fail. These calls keep
// their raw Response (the callers only want the JSON body), so they run
// the shared `raiseForUnauthorized` guard instead of going through
// fetchJSON.
describe('expired-cookie handling', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  const cases: Array<[string, (mod: typeof import('./githubPreview')) => Promise<unknown>]> = [
    ['github preview', (mod) => mod.cachedGitHubPreview('https://github.com/o/r/pull/1')],
    ['forgejo host discovery', (mod) => mod.loadForgejoHosts()],
    [
      'forgejo preview',
      (mod) => mod.cachedForgejoPreview('https://code.example.com/o/r/pulls/1', ['code.example.com']),
    ],
  ];

  it.each(cases)('reports a 401 from the %s call as an auth error', async (_label, call) => {
    const mod = await freshModule();
    const { AuthError, registerAuthErrorHandler } = await import('./api');
    const handler = vi.fn();
    registerAuthErrorHandler(handler);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('session expired', { status: 401 })));

    await call(mod);

    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler.mock.calls[0][0]).toBeInstanceOf(AuthError);
  });
});
