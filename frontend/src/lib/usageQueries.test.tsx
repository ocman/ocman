// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { useAgentRunHours, useUIUsage } from './queries';

afterEach(() => vi.unstubAllGlobals());

it('fetches daily usage without project scope and hourly runtime with project scope', async () => {
  const fetch = vi.fn(async (url: string) => new Response(JSON.stringify(url.includes('ui-usage')
    ? [{ date: '2026-10-09', activeSeconds: 3600 }]
    : [{ timestamp: 3_600_000, minutes: 90 }]), { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const { result, unmount } = renderHook(() => ({ usage: useUIUsage(7), agents: useAgentRunHours({ days: 7, dir: '/repo' }) }), { wrapper });
  await waitFor(() => expect(result.current.usage.isSuccess && result.current.agents.isSuccess).toBe(true));
  expect(result.current.usage.data).toEqual([{ date: '2026-10-09', activeSeconds: 3600 }]);
  expect(result.current.agents.data).toEqual([{ timestamp: 3_600_000, minutes: 90 }]);
  expect(fetch).toHaveBeenCalledWith('/api/analytics/ui-usage?days=7', { signal: expect.any(AbortSignal) });
  expect(fetch).toHaveBeenCalledWith('/api/analytics/agent-run-hours?days=7&dir=%2Frepo', { signal: expect.any(AbortSignal) });
  unmount();
  client.clear();
});
