// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { api, type SessionDetail } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { PREFETCH_DELAY_MS, prefetchSession, useHoverPrefetch } from './sessionPrefetch';

function detail(id: string, platform = 'opencode'): SessionDetail {
  return {
    session: { id, platform, messageCount: 4 },
    messages: [],
    parts: [],
    totalMessages: 0,
    contextTokenCount: 7,
  } as unknown as SessionDetail;
}

beforeEach(() => {
  useApiStore.setState({ sessionCache: new Map(), sessionCacheOrder: [] });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('prefetchSession', () => {
  it('caches the first page with the same shape useSession writes', async () => {
    const spy = vi.spyOn(api, 'session').mockResolvedValue(detail('a'));
    await prefetchSession('a', 'opencode');
    // peek (last arg) so a hover never unarchives the session.
    expect(spy).toHaveBeenCalledWith('a', 30, 0, undefined, undefined, true);
    const cached = useApiStore.getState().getCachedSession('a');
    expect(cached?.totalMessages).toBe(4);
    expect(cached?.session.contextTokenCount).toBe(7);
  });

  it('trims an oversized response to the newest page', async () => {
    const messages = Array.from({ length: 35 }, (_, i) => ({ id: `m${i}`, sessionId: 'a', timeCreated: i, data: { role: 'user' } }));
    const parts = messages.map((m) => ({ id: `${m.id}-p`, messageId: m.id, sessionId: 'a', data: { type: 'text', text: '' } }));
    vi.spyOn(api, 'session').mockResolvedValue({ ...detail('a'), messages, parts } as unknown as SessionDetail);
    await prefetchSession('a');
    const cached = useApiStore.getState().getCachedSession('a')!;
    expect(cached.messages).toHaveLength(30);
    expect(cached.messages[0].id).toBe('m5');
    expect(cached.parts).toHaveLength(30);
  });

  it('routes remote sessions to their owner', async () => {
    const spy = vi.spyOn(api, 'session').mockResolvedValue(detail('a', 'r-m2:opencode'));
    await prefetchSession('a', 'r-m2:opencode');
    expect(spy).toHaveBeenCalledWith('a', 30, 0, undefined, 'r-m2:opencode', true);
  });

  it('skips cached and in-flight sessions', async () => {
    let resolve: (d: SessionDetail) => void = () => {};
    const spy = vi.spyOn(api, 'session').mockImplementation(() => new Promise((r) => { resolve = r; }));
    const first = prefetchSession('a');
    void prefetchSession('a');
    resolve(detail('a'));
    await first;
    await prefetchSession('a');
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it('does not overwrite an entry the open session cached meanwhile', async () => {
    let resolve: (d: SessionDetail) => void = () => {};
    vi.spyOn(api, 'session').mockImplementation(() => new Promise((r) => { resolve = r; }));
    const pending = prefetchSession('a');
    const fresh = { ...detail('a'), totalMessages: 99 };
    useApiStore.getState().setCachedSession('a', fresh);
    resolve(detail('a'));
    await pending;
    expect(useApiStore.getState().getCachedSession('a')?.totalMessages).toBe(99);
  });

  it('swallows failures and allows a later attempt', async () => {
    const spy = vi.spyOn(api, 'session').mockRejectedValueOnce(new Error('boom')).mockResolvedValue(detail('a'));
    await prefetchSession('a');
    expect(useApiStore.getState().getCachedSession('a')).toBeNull();
    await prefetchSession('a');
    expect(spy).toHaveBeenCalledTimes(2);
    expect(useApiStore.getState().getCachedSession('a')).not.toBeNull();
  });
});

describe('useHoverPrefetch', () => {
  it('prefetches after the dwell and cancels a short hover', async () => {
    vi.useFakeTimers();
    const spy = vi.spyOn(api, 'session').mockResolvedValue(detail('a'));
    const { result, unmount } = renderHook(() => useHoverPrefetch('a'));

    act(() => result.current.onPointerEnter());
    act(() => vi.advanceTimersByTime(PREFETCH_DELAY_MS - 1));
    act(() => result.current.onPointerLeave());
    act(() => vi.advanceTimersByTime(PREFETCH_DELAY_MS));
    expect(spy).not.toHaveBeenCalled();

    act(() => result.current.onFocus());
    await act(async () => vi.advanceTimersByTime(PREFETCH_DELAY_MS));
    expect(spy).toHaveBeenCalledTimes(1);

    // An unmount mid-dwell must not fire a stray fetch.
    useApiStore.setState({ sessionCache: new Map(), sessionCacheOrder: [] });
    act(() => result.current.onPointerEnter());
    unmount();
    act(() => vi.advanceTimersByTime(PREFETCH_DELAY_MS));
    expect(spy).toHaveBeenCalledTimes(1);
  });
});
