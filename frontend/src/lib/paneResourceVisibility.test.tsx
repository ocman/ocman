// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderHookWithQuery } from '../test/renderWithQuery';
import { useUpstreams } from './useUpstreams';
import { useUpstreamList } from './useUpstreamList';
import * as upstream from './upstreamApi';

const snapshot = [{ remote: 'origin', host: 'github.com', type: 'github' as const, repo: 'a/repo' }];

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

// Real browsers bubble visibilitychange to window, where react-query listens.
function setHidden(hidden: boolean) {
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(hidden);
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue(hidden ? 'hidden' : 'visible');
  document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
}

it('retains the upstream discovery snapshot while hidden and during resume', async () => {
  const fetcher = vi.spyOn(upstream, 'fetchUpstreams').mockResolvedValue(snapshot);
  const { result } = renderHookWithQuery(() => useUpstreams('/repo', 'local'));
  await waitFor(() => expect(result.current.upstreams).toBe(snapshot));
  act(() => setHidden(true));
  expect(result.current.upstreams).toBe(snapshot);
  fetcher.mockReturnValue(new Promise(() => {}));
  act(() => setHidden(false));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  expect(result.current.upstreams).toBe(snapshot);
  expect(result.current.ready).toBe(true);
});

it('retains the same-resource discovery snapshot when resume fails', async () => {
  const fetcher = vi.spyOn(upstream, 'fetchUpstreams').mockResolvedValue(snapshot);
  const { result, rerender } = renderHookWithQuery(({ owner }) => useUpstreams('/repo', owner), { initialProps: { owner: 'owner-a' } });
  await waitFor(() => expect(result.current.upstreams).toBe(snapshot));
  act(() => setHidden(true));
  fetcher.mockRejectedValue(new Error('offline'));
  act(() => setHidden(false));
  await waitFor(() => expect(result.current.error).toBe('offline'));
  expect(result.current.upstreams).toBe(snapshot);
  rerender({ owner: 'owner-b' });
  await waitFor(() => expect(result.current.error).toBe('offline'));
  expect(result.current.upstreams).toEqual([]);
});

it('keeps the PR page and rows across backgrounding, without hidden requests', async () => {
  let hidden = false;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  const fetchPRs = vi.spyOn(upstream, 'fetchPRs').mockImplementation(async (params) => ({ prs: [{ number: params.page, title: 'Row' }], pagination: { page: params.page, hasMore: true }, rateLimit: { limited: false } }) as never);
  const { result } = renderHook(() => useUpstreamList({ kind: 'prs', dir: '/repo', remoteId: 'local', remote: 'origin', state: 'open', mine: undefined, enabled: true }));
  await waitFor(() => expect(result.current.items).toHaveLength(1));
  act(() => result.current.setPage(2));
  await waitFor(() => expect(result.current.pagination.page).toBe(2));
  fetchPRs.mockClear();
  act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
  act(() => result.current.refresh());
  expect(fetchPRs).not.toHaveBeenCalled();
  expect(result.current.page).toBe(2);
  expect(result.current.items).toHaveLength(1);
  await act(async () => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
  expect(fetchPRs).toHaveBeenCalledTimes(1);
  expect(fetchPRs).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 }));
  expect(result.current.page).toBe(2);
});
