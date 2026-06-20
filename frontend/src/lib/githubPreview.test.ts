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

describe('parseGitHubUrl', () => {
  it('parses a PR URL', () => {
    const ref = parseGitHubUrl('https://github.com/aspect-analytics/weave-agent/pull/15');
    expect(ref).toEqual({ kind: 'pr', owner: 'aspect-analytics', repo: 'weave-agent', number: 15 });
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
      'plus https://code.example.com/a/b/issues/2.';
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
      title: 'My PR',
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

  it('refresh bypasses the cache and refetches', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ title: 'Fresh', state: 'open' }));
    vi.stubGlobal('fetch', fetchMock);

    const url = 'https://github.com/o/r/pull/11';
    await mod.cachedGitHubPreview(url);
    await mod.refreshGitHubPreview(url);
    expect(fetchMock).toHaveBeenCalledTimes(2);
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
    expect(data).toMatchObject({ kind: 'pr', title: 'FJ PR', repo: 'o/r', author: 'carol' });
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

  it('refresh refetches and returns mapped data', async () => {
    const mod = await freshModule();
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ title: 'C', state: 'open' }));
    vi.stubGlobal('fetch', fetchMock);
    const url = 'https://code.example.com/o/r/pulls/7';
    await mod.cachedForgejoPreview(url, hosts);
    const data = await mod.refreshForgejoPreview(url, hosts);
    expect(data).toMatchObject({ kind: 'pr', title: 'C' });
    expect(fetchMock).toHaveBeenCalledTimes(2);
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
