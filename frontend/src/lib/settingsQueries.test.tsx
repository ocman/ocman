// @vitest-environment jsdom
import type { ReactNode } from 'react';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, expect, it, vi } from 'vitest';
import { api } from './api';
import { useHostCapabilities, useRemotes } from './settingsQueries';

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>
);

afterEach(() => vi.restoreAllMocks());

it('shows no remotes on a single-host instance, whose remote list errors', async () => {
  vi.spyOn(api, 'listRemotes').mockRejectedValue(new Error('HTTP 404'));
  const { result } = renderHook(() => useRemotes(), { wrapper });
  await waitFor(() => expect(result.current.data).toEqual([]));
  expect(result.current.isError).toBe(false);
});

it('reads only the host entries from the capabilities response', async () => {
  vi.spyOn(api, 'capabilities').mockResolvedValue({ platforms: [] } as never);
  const { result } = renderHook(() => useHostCapabilities(), { wrapper });
  await waitFor(() => expect(result.current.data).toEqual([]));
});
