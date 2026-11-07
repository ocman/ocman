// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { useRunningSessionCount } from './queries';
import * as events from './useGlobalEvents';

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.useRealTimers(); });

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

it('refreshes on events and reconnect, without five-second polling', async () => {
  let changed: (() => void) | undefined;
  let connected: (() => void) | undefined;
  const stop = vi.fn();
  vi.spyOn(events, 'onSessionChanged').mockImplementation(cb => { changed = () => cb('s', undefined, undefined, 'p'); return stop; });
  vi.spyOn(events, 'onSseConnect').mockImplementation(cb => { connected = cb; return stop; });
  const fetch = vi.fn(async () => new Response(JSON.stringify({ count: 2 }), { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const view = renderHook(() => useRunningSessionCount(true), { wrapper });
  try {
    await waitFor(() => expect(view.result.current.data).toEqual({ count: 2 }));
    vi.useFakeTimers();
    await act(async () => { await vi.advanceTimersByTimeAsync(5001); });
    expect(fetch).toHaveBeenCalledTimes(1);
    act(() => { changed?.(); changed?.(); });
    await act(async () => { await vi.advanceTimersByTimeAsync(200); });
    expect(fetch).toHaveBeenCalledTimes(2);
    act(() => connected?.());
    await act(async () => { await vi.advanceTimersByTimeAsync(200); });
    expect(fetch).toHaveBeenCalledTimes(3);
  } finally { view.unmount(); client.clear(); }
  expect(stop).toHaveBeenCalledTimes(2);
});
