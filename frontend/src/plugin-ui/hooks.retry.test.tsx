// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { callPluginUI } from '../lib/pluginUI';
import { PluginCallError } from '../lib/pluginCallError';
import { PluginHostContext } from './context';
import { usePluginCall, usePluginMutation } from './hooks';

vi.mock('../lib/pluginUI', () => ({ callPluginUI: vi.fn(), pluginUIQueryKey: (id: string) => ['plugin-ui-call', id] }));
afterEach(() => vi.clearAllMocks());

function setup() {
  // Host/client defaults must never enable implicit plugin request replay.
  const client = new QueryClient({ defaultOptions: { queries: { retry: 3, retryDelay: 0 }, mutations: { retry: 3, retryDelay: 0 } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>
    <PluginHostContext.Provider value={{ pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'local', basePath: '/plugins/org.example.catalog', runAction: async () => {} }}>
      {children}
    </PluginHostContext.Provider>
  </QueryClientProvider>;
  return { client, wrapper };
}

it('passes status-aware bounded retry callbacks to cached reads', async () => {
  const error = new PluginCallError(503, 'unavailable', 0);
  vi.mocked(callPluginUI).mockRejectedValue(error);
  const retry = vi.fn((count: number, err: Error) => count < 2 && err instanceof PluginCallError && err.status === 503);
  const retryDelay = vi.fn(() => 0);
  const { client, wrapper } = setup();
  const { result } = renderHook(() => usePluginCall('items.list', {}, { retry, retryDelay }), { wrapper });
  await waitFor(() => expect(result.current.isError).toBe(true));
  expect(callPluginUI).toHaveBeenCalledTimes(3);
  expect(retry.mock.calls).toEqual([[0, error], [1, error], [2, error]]);
  expect(retryDelay).toHaveBeenCalledWith(0, error);
  expect(retryDelay).toHaveBeenCalledWith(1, error);
  const query = client.getQueryCache().getAll()[0];
  expect(query.options.retry).toBe(retry);
  expect(query.options.retryDelay).toBe(retryDelay);
  client.clear();
});

it.each([400, 403, 500])('lets a read policy decline retries for HTTP %i', async (status) => {
  vi.mocked(callPluginUI).mockRejectedValue(new PluginCallError(status, null, null));
  const { client, wrapper } = setup();
  const { result } = renderHook(() => usePluginCall('documents.list', {}, {
    retry: (count, error) => count < 2 && error instanceof PluginCallError && error.status === 503,
    retryDelay: 0,
  }), { wrapper });
  await waitFor(() => expect(result.current.isError).toBe(true));
  expect(callPluginUI).toHaveBeenCalledOnce();
  client.clear();
});

it('disables default retries for queries and mutations even under retrying client defaults', async () => {
  const error = new PluginCallError(503, 'unavailable', null);
  vi.mocked(callPluginUI).mockRejectedValue(error);
  const { client, wrapper } = setup();
  const query = renderHook(() => usePluginCall('items.list'), { wrapper });
  await waitFor(() => expect(query.result.current.isError).toBe(true));
  expect(callPluginUI).toHaveBeenCalledOnce();
  const mutation = renderHook(() => usePluginMutation('items.save'), { wrapper });
  await act(async () => { await expect(mutation.result.current.mutateAsync({ value: 1 })).rejects.toBe(error); });
  expect(callPluginUI).toHaveBeenCalledTimes(2);
  client.clear();
});

it('cancels the read signal and pending retry when the last observer unmounts', async () => {
  vi.mocked(callPluginUI).mockRejectedValue(new PluginCallError(503, 'unavailable', null));
  const { client, wrapper } = setup();
  const { unmount } = renderHook(() => usePluginCall('items.list', {}, { retry: 2, retryDelay: 60_000 }), { wrapper });
  await waitFor(() => expect(callPluginUI).toHaveBeenCalledOnce());
  const signal = vi.mocked(callPluginUI).mock.calls[0][3];
  unmount();
  expect(signal?.aborted).toBe(true);
  expect(client.getQueryCache().getAll()[0].state.fetchStatus).toBe('idle');
  expect(callPluginUI).toHaveBeenCalledOnce();
  client.clear();
});
