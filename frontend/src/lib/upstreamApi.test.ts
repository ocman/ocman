import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  fetchUpstreams,
  fetchPRs,
  fetchIssues,
  fetchPRChecks,
  fetchForgeUser,
  postHandle,
  UpstreamApiError,
  type ListPRsResponse,
} from './upstreamApi';
import { AuthError, registerAuthErrorHandler } from './api';

describe('upstreamApi', () => {
  let fetchSpy: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    fetchSpy = vi.fn();
    globalThis.fetch = fetchSpy as unknown as typeof fetch;
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('fetchUpstreams', () => {
    it('returns the upstreams array on 200', async () => {
      fetchSpy.mockResolvedValue(
        new Response(
          JSON.stringify({
            upstreams: [{ remote: 'origin', host: 'github.com', type: 'github', repo: 'a/b' }],
          }),
          { status: 200 },
        ),
      );
      const got = await fetchUpstreams('/abs/dir');
      expect(got).toHaveLength(1);
      expect(got[0].remote).toBe('origin');
    });

    it('treats 404 (not a git repo) as empty', async () => {
      fetchSpy.mockResolvedValue(new Response('not a repo', { status: 404 }));
      const got = await fetchUpstreams('/abs/dir');
      expect(got).toEqual([]);
    });

    it('throws on other non-2xx', async () => {
      fetchSpy.mockResolvedValue(new Response('boom', { status: 500 }));
      await expect(fetchUpstreams('/abs/dir')).rejects.toThrow(/500/);
    });
  });

  describe('fetchPRs', () => {
    it('builds query and parses response', async () => {
      const body: ListPRsResponse = {
        prs: [
          {
            number: 42,
            title: 't',
            body: 'b',
            author: 'a',
            status: 'open',
            updatedAt: '2026-05-01T00:00:00Z',
            labels: [],
            assignees: [],
            requestedReviewers: [],
            branch: 'br',
            url: 'u',
            host: 'github.com',
            repo: 'a/b',
            crossFork: false,
          },
        ],
        pagination: { page: 1, hasMore: false },
        rateLimit: { limited: false },
      };
      fetchSpy.mockResolvedValue(new Response(JSON.stringify(body), { status: 200 }));

      const got = await fetchPRs({ dir: '/x', remote: 'origin', state: 'open', mine: undefined, page: 1 });
      expect(got.prs).toHaveLength(1);
      const calledUrl = String(fetchSpy.mock.calls[0][0]);
      expect(calledUrl).toContain('/api/project/prs');
      expect(calledUrl).toContain('dir=%2Fx');
      expect(calledUrl).toContain('remote=origin');
      expect(calledUrl).toContain('state=open');
      expect(calledUrl).toContain('page=1');
      expect(calledUrl).not.toContain('mine=');
    });

    it('includes mine when provided', async () => {
      fetchSpy.mockResolvedValue(
        new Response(JSON.stringify({ prs: [], pagination: { page: 1, hasMore: false }, rateLimit: { limited: false } }), { status: 200 }),
      );
      await fetchPRs({ dir: '/x', remote: 'origin', state: 'open', mine: 'alice', page: 1 });
      const calledUrl = String(fetchSpy.mock.calls[0][0]);
      expect(calledUrl).toContain('mine=alice');
    });

    it('throws UpstreamApiError with envelope on 4xx', async () => {
      fetchSpy.mockResolvedValue(
        new Response(
          JSON.stringify({ error: { code: 'rate_limited', message: 'slow down', retryAfter: '2026-05-21T14:08:00Z' } }),
          { status: 429 },
        ),
      );
      const err = await fetchPRs({ dir: '/x', remote: 'origin', state: 'open', mine: undefined, page: 1 })
        .then(() => null, (e) => e);
      expect(err).toBeInstanceOf(UpstreamApiError);
      expect((err as UpstreamApiError).envelope?.error.code).toBe('rate_limited');
      expect((err as UpstreamApiError).status).toBe(429);
    });
  });

  describe('postHandle', () => {
    it('posts JSON and returns the parsed response on 200', async () => {
      fetchSpy.mockResolvedValue(
        new Response(JSON.stringify({ childSessionId: 'abc', mode: 'session' }), { status: 200 }),
      );
      const got = await postHandle({
        dir: '/x',
        remote: 'origin',
        type: 'pr',
        number: 1,
        mode: 'session',
      });
      expect(got.childSessionId).toBe('abc');
      const [, init] = fetchSpy.mock.calls[0];
      expect((init as RequestInit).method).toBe('POST');
    });

    it('surfaces the requires_fetch envelope on 409', async () => {
      fetchSpy.mockResolvedValue(
        new Response(
          JSON.stringify({
            error: { code: 'requires_fetch', message: 'fork', fetchTarget: 'ocman/pr-7' },
          }),
          { status: 409 },
        ),
      );
      const err = await postHandle({
        dir: '/x',
        remote: 'origin',
        type: 'pr',
        number: 7,
        mode: 'worktree',
        fetchHead: false,
      }).then(() => null, (e) => e);
      expect(err).toBeInstanceOf(UpstreamApiError);
      expect((err as UpstreamApiError).envelope?.error.code).toBe('requires_fetch');
    });
  });

  describe('fetchIssues', () => {
    it('builds the query and parses the response', async () => {
      fetchSpy.mockResolvedValue(
        new Response(JSON.stringify({ issues: [{ number: 7, title: 'bug' }] }), { status: 200 }),
      );
      const got = await fetchIssues({
        dir: '/x',
        remote: 'origin',
        state: 'open',
        mine: 'alice',
        page: 2,
      });
      expect(got.issues).toHaveLength(1);
      const [url] = fetchSpy.mock.calls[0];
      expect(String(url)).toContain('/api/project/issues?');
      expect(String(url)).toContain('mine=alice');
      expect(String(url)).toContain('page=2');
    });

    it('throws an UpstreamApiError on non-2xx', async () => {
      fetchSpy.mockResolvedValue(
        new Response(JSON.stringify({ error: { code: 'x', message: 'no' } }), { status: 500 }),
      );
      await expect(
        fetchIssues({ dir: '/x', remote: 'origin', state: 'open', mine: undefined, page: 1 }),
      ).rejects.toBeInstanceOf(UpstreamApiError);
    });
  });

  describe('fetchPRChecks', () => {
    it('builds the query and parses the CI status', async () => {
      fetchSpy.mockResolvedValue(
        new Response(
          JSON.stringify({ state: 'success', checks: [{ name: 'build', state: 'success' }] }),
          { status: 200 },
        ),
      );
      const got = await fetchPRChecks({ dir: '/x', remote: 'origin', sha: 'abc123' });
      expect(got.state).toBe('success');
      expect(got.checks).toHaveLength(1);
      const [url] = fetchSpy.mock.calls[0];
      expect(String(url)).toContain('/api/project/pr-checks?');
      expect(String(url)).toContain('sha=abc123');
    });

    it('throws an UpstreamApiError on failure', async () => {
      fetchSpy.mockResolvedValue(
        new Response(JSON.stringify({ error: { code: 'upstream_status', message: 'boom' } }), {
          status: 502,
        }),
      );
      await expect(
        fetchPRChecks({ dir: '/x', remote: 'origin', sha: 'abc123' }),
      ).rejects.toBeInstanceOf(UpstreamApiError);
    });
  });

  describe('fetchForgeUser', () => {
    it('returns the user on 200', async () => {
      fetchSpy.mockResolvedValue(
        new Response(JSON.stringify({ login: 'alice', host: 'github.com' }), { status: 200 }),
      );
      const got = await fetchForgeUser({ dir: '/x', remote: 'origin' });
      expect(got).toEqual({ login: 'alice', host: 'github.com' });
    });

    // The backend answers a forge-auth failure with an error envelope
    // (writeProjectListError), so that is what the fixture models.
    it('returns null on a forge 401 (unauthenticated against the forge)', async () => {
      fetchSpy.mockResolvedValue(
        new Response(JSON.stringify({ error: { code: 'auth_required', message: 'not authenticated' } }), { status: 401 }),
      );
      const got = await fetchForgeUser({ dir: '/x', remote: 'origin' });
      expect(got).toBeNull();
    });

    // A bare 401 is ocman's own auth middleware: the cookie expired, so
    // the app must reach the lockscreen rather than silently disabling
    // the "mine" filter.
    it('raises AuthError on a bare 401 (expired ocman session)', async () => {
      fetchSpy.mockResolvedValue(new Response('unauthorized', { status: 401 }));
      await expect(fetchForgeUser({ dir: '/x', remote: 'origin' })).rejects.toThrow(AuthError);
    });

    it('throws on other non-2xx', async () => {
      fetchSpy.mockResolvedValue(new Response('boom', { status: 500 }));
      await expect(fetchForgeUser({ dir: '/x', remote: 'origin' })).rejects.toThrow(/500/);
    });
  });

  // Regression: these helpers used raw fetch(), so only fetchJSON /
  // postJSON call throwForStatus and fan a 401 out to onAuthError. On an
  // expired cookie the panes rendered a generic error instead of
  // redirecting to the lockscreen.
  describe('expired ocman session', () => {
    let seen: AuthError[];
    let restore: ReturnType<typeof registerAuthErrorHandler>;
    beforeEach(() => {
      seen = [];
      restore = registerAuthErrorHandler((err) => { seen.push(err); });
    });
    afterEach(() => { registerAuthErrorHandler(restore); });

    const bare401 = () => new Response('unauthorized', { status: 401 });

    it('fetchUpstreams reports the auth error', async () => {
      fetchSpy.mockResolvedValue(bare401());
      await expect(fetchUpstreams('/x')).rejects.toThrow(AuthError);
      expect(seen).toHaveLength(1);
    });

    it('fetchPRs reports the auth error', async () => {
      fetchSpy.mockResolvedValue(bare401());
      await expect(fetchPRs({ dir: '/x', remote: 'origin', state: 'open', mine: undefined, page: 1 }))
        .rejects.toThrow(AuthError);
      expect(seen).toHaveLength(1);
    });

    it('fetchIssues reports the auth error', async () => {
      fetchSpy.mockResolvedValue(bare401());
      await expect(fetchIssues({ dir: '/x', remote: 'origin', state: 'open', mine: undefined, page: 1 }))
        .rejects.toThrow(AuthError);
      expect(seen).toHaveLength(1);
    });

    it('fetchPRChecks reports the auth error', async () => {
      fetchSpy.mockResolvedValue(bare401());
      await expect(fetchPRChecks({ dir: '/x', remote: 'origin', sha: 'abc' })).rejects.toThrow(AuthError);
      expect(seen).toHaveLength(1);
    });

    it('postHandle reports the auth error', async () => {
      fetchSpy.mockResolvedValue(bare401());
      await expect(postHandle({ dir: '/x', remote: 'origin', type: 'pr', number: 1, mode: 'session' }))
        .rejects.toThrow(AuthError);
      expect(seen).toHaveLength(1);
    });

    // A forge-level 401 carries an envelope and must NOT log the user out.
    it('leaves a forge 401 alone', async () => {
      fetchSpy.mockResolvedValue(
        new Response(JSON.stringify({ error: { code: 'auth_required', message: 'no token' } }), { status: 401 }),
      );
      await expect(fetchPRs({ dir: '/x', remote: 'origin', state: 'open', mine: undefined, page: 1 }))
        .rejects.toThrow(UpstreamApiError);
      expect(seen).toHaveLength(0);
    });
  });
});
