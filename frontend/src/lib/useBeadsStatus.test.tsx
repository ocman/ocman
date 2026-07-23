// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useBeadsStatus } from './useBeadsStatus';

function wrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('useBeadsStatus', () => {
  it('requests repositories from their local or remote owner', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{"available":false}', { status: 200 }),
    );
    const qc = client();
    const { rerender } = renderHook(
      ({ dir, remoteId }) => useBeadsStatus(dir, remoteId, false),
      { initialProps: { dir: undefined as string | undefined, remoteId: undefined as string | undefined }, wrapper: wrapper(qc) },
    );
    expect(fetchMock).not.toHaveBeenCalled();

    rerender({ dir: '/repo', remoteId: 'local' });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(String(fetchMock.mock.calls[0][0])).toContain('dir=%2Frepo');
    expect(String(fetchMock.mock.calls[0][0])).toContain('remoteId=local');

    rerender({ dir: '/repo', remoteId: 'abc' });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    expect(String(fetchMock.mock.calls[1][0])).toContain('remoteId=abc');
  });

  it('keeps the last tree when a discovered workspace becomes unhealthy', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify({ available: true, tickets: [{ id: 'bd-1', title: 'One', status: 'open', priority: 1 }] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ available: true, error: 'status_unavailable', tickets: [{ id: 'bd-flat', title: 'Flat', status: 'open', priority: 2 }] }), { status: 200 }));
    const qc = client();
    const { result } = renderHook(() => useBeadsStatus('/repo', 'local', false), { wrapper: wrapper(qc) });
    await waitFor(() => expect(result.current.data?.tickets).toHaveLength(1));

    await act(async () => { await result.current.refetch(); });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(result.current.data?.error).toBe('status_unavailable'));
    expect(result.current.data?.tickets?.[0].id).toBe('bd-1');
  });

  it('keeps a discovered workspace visible when discovery temporarily fails', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify({ available: true, tickets: [{ id: 'bd-1', title: 'One', status: 'open', priority: 1 }] }), { status: 200 }))
      .mockRejectedValueOnce(new Error('temporary failure'));
    const qc = client();
    const { result } = renderHook(() => useBeadsStatus('/repo', 'local', false), { wrapper: wrapper(qc) });
    await waitFor(() => expect(result.current.data?.tickets).toHaveLength(1));

    act(() => { void result.current.refetch(); });
    await waitFor(() => expect(result.current.error).toBeTruthy());
    expect(result.current.data).toMatchObject({ available: true, tickets: [{ id: 'bd-1' }] });
  });

  it('polls every 30 seconds only while visible and supports manual refresh', async () => {
    vi.useFakeTimers();
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{"available":true,"tickets":[]}', { status: 200 }),
    );
    const qc = client();
    const { result, rerender } = renderHook(
      ({ visible }) => useBeadsStatus('/repo', 'local', visible),
      { initialProps: { visible: false }, wrapper: wrapper(qc) },
    );
    await act(async () => { await vi.runAllTicks(); });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    rerender({ visible: true });
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(fetchMock).toHaveBeenCalledTimes(2);

    await act(async () => { await result.current.refetch(); });
    expect(fetchMock).toHaveBeenCalledTimes(3);
    rerender({ visible: false });
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it('cancels the old repository request and never overlaps polling', async () => {
    vi.useFakeTimers();
    let resolveFirst!: (response: Response) => void;
    const first = new Promise<Response>((resolve) => { resolveFirst = resolve; });
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockImplementationOnce(() => first)
      .mockResolvedValue(new Response('{"available":true,"tickets":[]}', { status: 200 }));
    const qc = client();
    const { rerender } = renderHook(
      ({ dir }) => useBeadsStatus(dir, 'local', true),
      { initialProps: { dir: '/one' }, wrapper: wrapper(qc) },
    );
    await act(async () => { await vi.runAllTicks(); });
    const firstSignal = (fetchMock.mock.calls[0][1] as RequestInit).signal!;

    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(fetchMock).toHaveBeenCalledTimes(1);

    rerender({ dir: '/two' });
    await act(async () => { await vi.runAllTicks(); });
    expect(firstSignal.aborted).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    resolveFirst(new Response('{"available":true,"tickets":[]}', { status: 200 }));
  });
});
