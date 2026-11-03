// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { postJSON } = vi.hoisted(() => ({ postJSON: vi.fn() }));
vi.mock('./api.requests', () => ({ postJSON }));

import { fileURLNeedsRefresh, refreshFileURL, resetFileURLRefreshes, useFreshFileURL } from './fileUrls';

const now = Date.UTC(2026, 10, 1);
const inDays = (days: number) => Math.floor((now + days * 86_400_000) / 1000);

describe('fileURLNeedsRefresh', () => {
  it.each([
    [`/api/file/L3g.${inDays(6)}.sig`, false],
    [`/api/file/L3g.${inDays(0.5)}.sig`, true],
    [`/api/file/L3g.${inDays(-1)}.sig`, true],
    ['/api/file/L3g.sig', true],
    ['/api/file/token', false],
    ['/api/file/L3g.x.sig', false],
    ['/api/file/a.b.c.d', false],
    ['/api/artifacts/a/files/0', false],
    ['https://example.com/api/file/L3g.sig', false],
  ])('%s -> %s', (href, want) => {
    expect(fileURLNeedsRefresh(href, now)).toBe(want);
  });
});

describe('useFreshFileURL', () => {
  beforeEach(() => {
    postJSON.mockReset();
    resetFileURLRefreshes();
  });

  it('keeps fresh and non-file URLs without asking the server', () => {
    const fresh = `/api/file/L3g.${Math.floor(Date.now() / 1000) + 6 * 86_400}.sig`;
    expect(renderHook(() => useFreshFileURL(fresh)).result.current).toBe(fresh);
    expect(renderHook(() => useFreshFileURL(undefined)).result.current).toBeUndefined();
    expect(postJSON).not.toHaveBeenCalled();
  });

  it('swaps a stale URL for the re-minted one', async () => {
    postJSON.mockResolvedValue({ url: '/api/file/L3g.99.new' });
    const { result } = renderHook(() => useFreshFileURL('/api/file/L3g.sig'));
    await waitFor(() => expect(result.current).toBe('/api/file/L3g.99.new'));
    expect(postJSON).toHaveBeenCalledWith('/api/files/refresh', { url: '/api/file/L3g.sig' });
  });

  it('refreshes on the owner and keeps caches isolated between owners', async () => {
    postJSON.mockResolvedValue({ url: '/api/file/L3g.99.new' });
    for (const owner of ['one', 'two']) {
      const prefix = `/api/remote-resources/${owner}`;
      await expect(refreshFileURL(`${prefix}/api/file/L3g.sig`)).resolves.toBe(`${prefix}/api/file/L3g.99.new`);
      expect(postJSON).toHaveBeenCalledWith(`${prefix}/api/files/refresh`, { url: '/api/file/L3g.sig' });
      expect(fileURLNeedsRefresh(`${prefix}/api/file/L3g.sig`)).toBe(true);
    }
    expect(postJSON).toHaveBeenCalledTimes(2);
  });

  it('shares one refresh per URL and retries after a failure', async () => {
    postJSON.mockRejectedValueOnce(new Error('403'));
    const { result } = renderHook(() => useFreshFileURL('/api/file/L3g.sig'));
    await expect(refreshFileURL('/api/file/L3g.sig')).rejects.toThrow('403');
    expect(result.current).toBe('/api/file/L3g.sig');
    expect(postJSON).toHaveBeenCalledTimes(1);

    postJSON.mockResolvedValue({ url: '/api/file/L3g.99.new' });
    await expect(refreshFileURL('/api/file/L3g.sig')).resolves.toBe('/api/file/L3g.99.new');
    expect(postJSON).toHaveBeenCalledTimes(2);
  });

  it('re-mints again once the cached replacement itself nears expiry', async () => {
    vi.useFakeTimers({ now });
    try {
      const stale = '/api/file/L3g.sig';
      const first = `/api/file/L3g.${inDays(7)}.one`;
      const second = `/api/file/L3g.${inDays(14)}.two`;
      postJSON.mockResolvedValueOnce({ url: first }).mockResolvedValueOnce({ url: second });
      await expect(refreshFileURL(stale)).resolves.toBe(first);
      // Still fresh a day later: reused without asking the server again.
      vi.setSystemTime(now + 86_400_000);
      await expect(refreshFileURL(stale)).resolves.toBe(first);
      expect(postJSON).toHaveBeenCalledTimes(1);
      // A long-lived dashboard a week on must not reuse the expired one.
      vi.setSystemTime(now + 7 * 86_400_000);
      await expect(refreshFileURL(stale)).resolves.toBe(second);
      expect(postJSON).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});
