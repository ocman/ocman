import { QueryClient, QueryObserver } from '@tanstack/react-query';
import { describe, expect, it, vi } from 'vitest';
import { queryEventRefresh } from './queryEventRefresh';

describe('query event refresh', () => {
  it('debounces bursts, waits for in-flight reads, and keeps a trailing refresh', async () => {
    vi.useFakeTimers();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    let finish: (() => void) | undefined;
    let signal: AbortSignal | undefined;
    const fetch = vi.fn().mockResolvedValue(['initial']);
    const observer = new QueryObserver(client, { queryKey: ['sessions', {}], queryFn: ({ signal: s }) => { signal = s; return fetch(); } });
    const unsubscribe = observer.subscribe(() => {});
    const refresh = queryEventRefresh(client, ['sessions']);
    try {
      await vi.advanceTimersByTimeAsync(0);
      fetch.mockImplementationOnce(() => new Promise(resolve => { finish = () => resolve(['renamed']); }));
      for (let i = 0; i < 10; i++) refresh.schedule();
      expect(fetch).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(150);
      expect(fetch).toHaveBeenCalledTimes(2);
      for (let i = 0; i < 10; i++) refresh.schedule();
      await vi.advanceTimersByTimeAsync(150);
      expect(fetch).toHaveBeenCalledTimes(2);
      expect(signal?.aborted).toBe(false);
      finish?.();
      await vi.advanceTimersByTimeAsync(150);
      expect(fetch).toHaveBeenCalledTimes(3);
      refresh.schedule();
      await vi.advanceTimersByTimeAsync(150);
      expect(fetch).toHaveBeenCalledTimes(4);
      refresh.schedule();
      refresh.dispose();
      await vi.advanceTimersByTimeAsync(150);
      expect(fetch).toHaveBeenCalledTimes(4);
    } finally { refresh.dispose(); unsubscribe(); client.clear(); vi.useRealTimers(); }
  });

  it('waits for a preexisting fetch and then reads the event instead of losing it', async () => {
    vi.useFakeTimers();
    const client = new QueryClient();
    let finish: (() => void) | undefined;
    const fetch = vi.fn().mockImplementationOnce(() => new Promise(resolve => { finish = () => resolve([]); })).mockResolvedValue(['new']);
    const observer = new QueryObserver(client, { queryKey: ['inbox'], queryFn: () => fetch() });
    const unsubscribe = observer.subscribe(() => {});
    const refresh = queryEventRefresh(client, ['inbox']);
    try {
      refresh.schedule();
      await vi.advanceTimersByTimeAsync(150);
      expect(fetch).toHaveBeenCalledTimes(1);
      finish?.();
      await vi.advanceTimersByTimeAsync(0);
      expect(fetch).toHaveBeenCalledTimes(2);
      expect(client.getQueryData(['inbox'])).toEqual(['new']);
    } finally { refresh.dispose(); unsubscribe(); client.clear(); vi.useRealTimers(); }
  });
});
