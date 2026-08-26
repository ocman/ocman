// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react';
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

it('clears pagination and rate limits while refreshing', async () => {
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

  result.current.refresh();
  await waitFor(() => expect(result.current.loading).toBe(true));
  expect(result.current.pagination).toEqual({ page: 1, hasMore: false });
  expect(result.current.rateLimit).toEqual({ limited: false });
});
