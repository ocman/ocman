// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { usePluginPanes, usePluginPaneTree, type PluginPane } from './pluginPanes';

const pane: PluginPane = { pluginId: 'org.example.tree', ownerId: 'local', pane: { id: 'items', label: 'Tree' } };
function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

it('does not list panes before the owner resolves', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('[]'));
  const { rerender } = renderHook(({ owner }) => usePluginPanes(owner), { initialProps: { owner: undefined as string | undefined }, wrapper: wrapper() });
  expect(fetch).not.toHaveBeenCalled();
  rerender({ owner: 'machine' });
  await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
  expect(String(fetch.mock.calls[0][0])).toContain('ownerId=machine');
});

it('keeps the last tree on warnings and transport errors', async () => {
  vi.spyOn(globalThis, 'fetch')
    .mockResolvedValueOnce(new Response('{"available":true,"nodes":[{"id":"good","title":"Good"}]}'))
    .mockResolvedValueOnce(new Response('{"available":true,"warning":true,"nodes":[{"id":"flat","title":"Flat"}]}'))
    .mockRejectedValueOnce(new Error('offline'));
  const { result } = renderHook(() => usePluginPaneTree(pane, '/repo'), { wrapper: wrapper() });
  await waitFor(() => expect(result.current.data?.nodes?.[0].id).toBe('good'));
  await act(async () => { await result.current.refetch(); });
  await waitFor(() => expect(result.current.data?.warning).toBe(true));
  expect(result.current.data?.nodes?.[0].id).toBe('good');
  act(() => { void result.current.refetch(); });
  await waitFor(() => expect(result.current.error).toBeTruthy());
  expect(result.current.data?.nodes?.[0].id).toBe('good');
});

it('polls only while mounted and does not overlap requests', async () => {
  vi.useFakeTimers();
  const fetch = vi.spyOn(globalThis, 'fetch')
    .mockResolvedValueOnce(new Response('{"available":true,"nodes":[]}'))
    .mockImplementation(() => new Promise<Response>(() => {}));
  const { unmount } = renderHook(() => usePluginPaneTree(pane, '/repo'), { wrapper: wrapper() });
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(fetch).toHaveBeenCalledTimes(1);
  await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
  expect(fetch).toHaveBeenCalledTimes(2);
  await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
  expect(fetch).toHaveBeenCalledTimes(2);
  const signal = fetch.mock.calls[1][1]?.signal as AbortSignal;
  unmount();
  expect(signal.aborted).toBe(true);
  await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
  expect(fetch).toHaveBeenCalledTimes(2);
});

it('cancels an old project read when the directory or owner changes', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise<Response>(() => {}));
  const { rerender } = renderHook(({ dir, owner }) => usePluginPaneTree({ ...pane, ownerId: owner }, dir), {
    initialProps: { dir: '/one', owner: 'local' }, wrapper: wrapper(),
  });
  await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
  const signal = fetch.mock.calls[0][1]?.signal as AbortSignal;
  rerender({ dir: '/two', owner: 'machine' });
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  expect(signal.aborted).toBe(true);
  expect(String(fetch.mock.calls[1][0])).toContain('ownerId=machine');
  expect(String(fetch.mock.calls[1][0])).toContain('directory=%2Ftwo');
});
