import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  fetchUpstreams,
  fetchPRs,
  postHandle,
  UpstreamApiError,
  type ListPRsResponse,
} from './upstreamApi';

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
});
