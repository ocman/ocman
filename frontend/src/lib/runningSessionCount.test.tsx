// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { useRunningSessionCount } from './queries';

afterEach(() => vi.unstubAllGlobals());

it('fetches only the compact count when enabled', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ count: 5 }), { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const { result, rerender, unmount } = renderHook((enabled) => useRunningSessionCount(enabled), { initialProps: false, wrapper });
  expect(fetch).not.toHaveBeenCalled();
  rerender(true);
  await waitFor(() => expect(result.current.data).toEqual({ count: 5 }));
  expect(fetch).toHaveBeenCalledWith('/api/sessions?view=running-count', expect.objectContaining({ signal: expect.any(AbortSignal) }));
  unmount();
  client.clear();
});
