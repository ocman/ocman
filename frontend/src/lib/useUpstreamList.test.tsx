// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import * as api from './upstreamApi';
import { useUpstreamList } from './useUpstreamList';

beforeEach(() => vi.restoreAllMocks());

it('clears rows when the project changes', async () => {
  const snapshots: Array<{ dir: string; titles: string[] }> = [];
  vi.spyOn(api, 'fetchPRs')
    .mockResolvedValueOnce({
      prs: [{ number: 7, title: 'old project' } as api.PR],
      pagination: { page: 1, hasMore: false },
      rateLimit: { limited: false },
    })
    .mockReturnValueOnce(new Promise(() => {}));

  const { result, rerender } = renderHook(
    ({ dir }) => {
      const value = useUpstreamList<api.PR>({
        kind: 'prs', dir, remoteId: 'local', remote: 'origin', state: 'open', mine: undefined, enabled: true,
      });
      snapshots.push({ dir, titles: value.items.map((item) => item.title) });
      return value;
    },
    { initialProps: { dir: '/old' } },
  );
  await waitFor(() => expect(result.current.items).toHaveLength(1));
  expect(result.current.pagination).toEqual({ page: 1, hasMore: false });

  rerender({ dir: '/new' });
  await waitFor(() => expect(result.current.loading).toBe(true));
  expect(result.current.items).toEqual([]);
  expect(result.current.pagination).toEqual({ page: 1, hasMore: false });
  expect(result.current.rateLimit).toEqual({ limited: false });
  expect(snapshots).not.toContainEqual({ dir: '/new', titles: ['old project'] });
});

it('retains rows and pagination while refreshing and clears stale rate limits', async () => {
  vi.spyOn(api, 'fetchPRs')
    .mockResolvedValueOnce({
      prs: [{ number: 7, title: 'old page' } as api.PR],
      pagination: { page: 1, hasMore: true },
      rateLimit: { limited: true, resetAt: '2026-08-26T12:00:00Z' },
    })
    .mockReturnValueOnce(new Promise(() => {}));

  const { result } = renderHook(() => useUpstreamList<api.PR>({
    kind: 'prs', dir: '/repo', remoteId: 'local', remote: 'origin', state: 'open', mine: undefined, enabled: true,
  }));
  await waitFor(() => expect(result.current.rateLimit.limited).toBe(true));

  const items = result.current.items;
  act(() => result.current.refresh());
  await waitFor(() => expect(result.current.loading).toBe(true));
  expect(result.current.items).toBe(items);
  expect(result.current.pagination).toEqual({ page: 1, hasMore: true });
  expect(result.current.rateLimit).toEqual({ limited: false });
});

it('does not expose rows from another owner or query', async () => {
  const base = {
    kind: 'prs' as const, dir: '/repo', remoteId: 'old-owner', remote: 'origin',
    state: 'open' as api.StateFilter, mine: undefined as string | undefined, enabled: true,
  };
  const changes = [
    { remoteId: 'new-owner' },
    { remote: 'mirror' },
    { state: 'closed' as api.StateFilter },
    { mine: 'alice' },
  ];

  for (const change of changes) {
    vi.restoreAllMocks();
    vi.spyOn(api, 'fetchPRs')
      .mockResolvedValueOnce({
        prs: [{ number: 7, title: 'old query' } as api.PR],
        pagination: { page: 1, hasMore: false }, rateLimit: { limited: false },
      })
      .mockReturnValueOnce(new Promise(() => {}));
    const snapshots: Array<{ key: string; titles: string[] }> = [];
    const hook = renderHook(
      ({ opts }) => {
        const value = useUpstreamList<api.PR>(opts);
        snapshots.push({ key: JSON.stringify(opts), titles: value.items.map((item) => item.title) });
        return value;
      },
      { initialProps: { opts: base } },
    );
    await waitFor(() => expect(hook.result.current.items).toHaveLength(1));

    const next = { ...base, ...change };
    hook.rerender({ opts: next });
    expect(hook.result.current.items).toEqual([]);
    expect(snapshots).not.toContainEqual({ key: JSON.stringify(next), titles: ['old query'] });
    hook.unmount();
  }
});

it('does not expose rows from the previous page', async () => {
  vi.spyOn(api, 'fetchPRs')
    .mockResolvedValueOnce({
      prs: [{ number: 7, title: 'page one' } as api.PR],
      pagination: { page: 1, hasMore: true }, rateLimit: { limited: false },
    })
    .mockReturnValueOnce(new Promise(() => {}));
  const snapshots: Array<{ page: number; titles: string[] }> = [];
  const { result } = renderHook(() => {
    const value = useUpstreamList<api.PR>({
      kind: 'prs', dir: '/repo', remoteId: 'local', remote: 'origin', state: 'open', mine: undefined, enabled: true,
    });
    snapshots.push({ page: value.page, titles: value.items.map((item) => item.title) });
    return value;
  });
  await waitFor(() => expect(result.current.items).toHaveLength(1));

  act(() => result.current.setPage(2));
  expect(result.current.items).toEqual([]);
  expect(snapshots).not.toContainEqual({ page: 2, titles: ['page one'] });
});

it.each(['prs', 'issues'] as const)('keeps %s visible on refresh failure and updates them on retry', async (kind) => {
  const item = { number: 7, title: 'original' } as api.PR & api.Issue;
  const pagination = { page: 1, hasMore: true };
  const rateLimit = { limited: false };
  const fetcher = kind === 'prs' ? vi.spyOn(api, 'fetchPRs') : vi.spyOn(api, 'fetchIssues');
  fetcher.mockResolvedValueOnce({ prs: [item], issues: [item], pagination, rateLimit })
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValueOnce({ prs: [{ ...item, title: 'updated' }], issues: [{ ...item, title: 'updated' }], pagination, rateLimit });
  const snapshots: string[][] = [];
  const { result } = renderHook(() => {
    const value = useUpstreamList<api.PR | api.Issue>({
      kind, dir: '/repo', remoteId: 'local', remote: 'origin', state: 'open', mine: undefined, enabled: true,
    });
    snapshots.push(value.items.map((item) => item.title));
    return value;
  });
  await waitFor(() => expect(result.current.items).toEqual([item]));
  snapshots.length = 0;
  act(() => result.current.refresh());
  await waitFor(() => expect(result.current.error?.message).toBe('offline'));
  expect(result.current.items).toEqual([item]);
  expect(result.current.pagination).toEqual(pagination);
  act(() => result.current.refresh());
  await waitFor(() => expect(result.current.items[0]?.title).toBe('updated'));
  expect(result.current.error).toBeNull();
  expect(snapshots).not.toContainEqual([]);
});
