// @vitest-environment jsdom
import type { ReactNode } from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { api } from './api';
import { useHostCapabilities, useRemotes } from './settingsQueries';

let client: QueryClient;
const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;

beforeEach(() => { client = new QueryClient({ defaultOptions: { queries: { retry: false } } }); });
afterEach(() => vi.restoreAllMocks());

it('keeps loaded remotes when a refetch fails and reports the error', async () => {
  const machine = { localId: 'r1', displayName: 'Build box' };
  const list = vi.spyOn(api, 'listRemotes').mockResolvedValue([machine] as never);
  // Read both fields during render: react-query re-renders only for tracked ones.
  const { result } = renderHook(() => { const q = useRemotes(); return { data: q.data, isError: q.isError }; }, { wrapper });
  await waitFor(() => expect(result.current.data).toEqual([machine]));
  list.mockRejectedValue(new Error('HTTP 502'));
  await act(async () => { await client.refetchQueries({ queryKey: ['remotes'] }); });
  await waitFor(() => expect(result.current.isError).toBe(true));
  expect(result.current.data).toEqual([machine]);
});

it('reads only the host entries from the capabilities response', async () => {
  vi.spyOn(api, 'capabilities').mockResolvedValue({ platforms: [] } as never);
  const { result } = renderHook(() => useHostCapabilities(), { wrapper });
  await waitFor(() => expect(result.current.data).toEqual([]));
});
