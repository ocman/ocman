// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createElement, type ReactNode } from 'react';
import { renderHook } from '@testing-library/react';
import { QueryClient, QueryClientProvider, QueryObserver } from '@tanstack/react-query';
import { INBOX_EVENT_DELAY_MS, INBOX_TIMEOUT_MS, inboxEventRefresh, useInbox } from './queries';
import { api } from './api';

vi.mock('./api', () => ({ api: { inbox: vi.fn() } }));

describe('inboxEventRefresh', () => {
  let client: QueryClient;
  let signals: AbortSignal[];
  let releases: (() => void)[];
  let unsubscribe: () => void;

  beforeEach(async () => {
    vi.useFakeTimers();
    signals = [];
    releases = [];
    client = new QueryClient();
    const observer = new QueryObserver(client, {
      queryKey: ['inbox'],
      queryFn: ({ signal }) => {
        signals.push(signal);
        return new Promise((resolve) => releases.push(() => resolve({ items: [] })));
      },
    });
    unsubscribe = observer.subscribe(() => {});
    releases.shift()!();
    await vi.advanceTimersByTimeAsync(0);
    signals = [];
  });

  afterEach(() => {
    unsubscribe();
    client.clear();
    vi.useRealTimers();
  });

  it('folds an event burst into one request and never aborts one in flight', async () => {
    const changed = inboxEventRefresh(client);
    for (let i = 0; i < 5; i++) changed();
    await vi.advanceTimersByTimeAsync(INBOX_EVENT_DELAY_MS);
    expect(signals).toHaveLength(1);

    for (let i = 0; i < 5; i++) changed();
    await vi.advanceTimersByTimeAsync(INBOX_EVENT_DELAY_MS * 2);
    expect(signals).toHaveLength(1);
    releases.shift()!();
    await vi.advanceTimersByTimeAsync(INBOX_EVENT_DELAY_MS);
    expect(signals).toHaveLength(2);
    releases.shift()!();
    await vi.advanceTimersByTimeAsync(INBOX_EVENT_DELAY_MS);
    expect(signals).toHaveLength(2);
    expect(signals.some((signal) => signal.aborted)).toBe(false);
  });

  it('refetches after a poll that was already in flight when the event arrived', async () => {
    void client.refetchQueries({ queryKey: ['inbox'] });
    expect(signals).toHaveLength(1);
    inboxEventRefresh(client)();
    await vi.advanceTimersByTimeAsync(INBOX_EVENT_DELAY_MS);
    expect(signals).toHaveLength(1);
    releases.shift()!();
    await vi.advanceTimersByTimeAsync(0);
    expect(signals).toHaveLength(2);
    expect(signals[0].aborted).toBe(false);
    releases.shift()!();
  });

  it('recovers a queued event refresh after the inbox request stalls', async () => {
    const inbox = vi.mocked(api.inbox);
    inbox.mockImplementationOnce((signal) => new Promise((_resolve, reject) => {
      signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
    }));
    inbox.mockResolvedValue({ items: [], unreadTotal: 0 } as never);
    const fresh = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client: fresh }, children);
    const { result, unmount } = renderHook(() => useInbox(), { wrapper });
    await vi.advanceTimersByTimeAsync(0);
    expect(inbox).toHaveBeenCalledTimes(1);
    inboxEventRefresh(fresh)();
    await vi.advanceTimersByTimeAsync(INBOX_TIMEOUT_MS + INBOX_EVENT_DELAY_MS);
    expect(inbox).toHaveBeenCalledTimes(2);
    expect(result.current.data).toEqual({ items: [], unreadTotal: 0 });
    unmount();
    fresh.clear();
  });
});
