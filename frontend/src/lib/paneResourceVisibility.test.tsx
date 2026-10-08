// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useAsyncResource } from './useAsyncResource';
import { useDebouncedSessionResource } from './useDebouncedSessionResource';
import { useUpstreamList } from './useUpstreamList';
import * as upstream from './upstreamApi';

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

it('retains the upstream discovery snapshot while hidden and during resume', async () => {
  let hidden = false;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  const fetcher = vi.fn().mockResolvedValue('snapshot');
  const { result } = renderHook(() => useAsyncResource({ fetcher, deps: [], initial: '', enabled: true }));
  await waitFor(() => expect(result.current.data).toBe('snapshot'));
  act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
  expect(result.current.data).toBe('snapshot');
  fetcher.mockReturnValue(new Promise(() => {}));
  act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(result.current.data).toBe('snapshot');
  expect(result.current.ready).toBe(true);
});

it('retains the same-resource discovery snapshot when resume fails', async () => {
  let hidden = false;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  const fetcher = vi.fn().mockResolvedValue('snapshot');
  const { result, rerender } = renderHook(({ key }) => useAsyncResource({ fetcher, deps: [key], initial: '', enabled: true }), { initialProps: { key: 'owner-a' } });
  await waitFor(() => expect(result.current.data).toBe('snapshot'));
  act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
  fetcher.mockRejectedValue(new Error('offline'));
  act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
  await waitFor(() => expect(result.current.error).toBe('offline'));
  expect(result.current.data).toBe('snapshot');
  act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
  rerender({ key: 'owner-b' });
  act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
  await waitFor(() => expect(result.current.error).toBe('offline'));
  expect(result.current.data).toBe('');
});

it('retains diff/info data, cancels dirty work while hidden, then refreshes', async () => {
  let hidden = false;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  const fetcher = vi.fn().mockResolvedValue('diff');
  const { result, rerender } = renderHook(({ tick }) => useDebouncedSessionResource('session', fetcher, '', 'error', { dirtyTick: tick }), { initialProps: { tick: 0 } });
  await waitFor(() => expect(result.current.data).toBe('diff'));
  vi.useFakeTimers();
  act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
  rerender({ tick: 1 });
  act(() => result.current.refresh());
  await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(result.current.data).toBe('diff');
  await act(async () => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
  expect(fetcher).toHaveBeenCalledTimes(2);
  expect(result.current.data).toBe('diff');
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
