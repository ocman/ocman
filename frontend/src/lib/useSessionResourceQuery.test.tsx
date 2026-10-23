// @vitest-environment jsdom
import { act, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderHookWithQuery } from '../test/renderWithQuery';
import { DIRTY_REFRESH_MS, useSessionResourceQuery, type SessionResourceOptions } from './useSessionResourceQuery';

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

function setup(fetch: (signal: AbortSignal) => Promise<string>, initial: { id?: string } & SessionResourceOptions = { id: 'session-1' }) {
  return renderHookWithQuery(
    ({ id, ...options }: { id?: string } & SessionResourceOptions) =>
      useSessionResourceQuery(id ? ['resource', id] : undefined, fetch, 'EMPTY', 'fallback error', options),
    { initialProps: initial },
  );
}

function setHidden(hidden: boolean) {
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(hidden);
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue(hidden ? 'hidden' : 'visible');
  document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
}

describe('useSessionResourceQuery', () => {
  it('returns the empty value without fetching when disabled', () => {
    const fetch = vi.fn().mockResolvedValue('data');
    const { result } = setup(fetch, { id: 'session-1', enabled: false });
    expect(result.current).toMatchObject({ data: 'EMPTY', loading: false, error: null });
    act(() => result.current.refresh());
    expect(fetch).not.toHaveBeenCalled();
  });

  it('returns null without fetching when there is no session', () => {
    const fetch = vi.fn().mockResolvedValue('data');
    const { result } = setup(fetch, {});
    expect(result.current).toMatchObject({ data: null, loading: false, error: null });
    act(() => result.current.refresh());
    expect(fetch).not.toHaveBeenCalled();
  });

  it('loads on mount and passes an AbortSignal', async () => {
    const fetch = vi.fn().mockResolvedValue('data');
    const { result } = setup(fetch);
    expect(result.current).toMatchObject({ data: null, loading: true });
    await waitFor(() => expect(result.current).toMatchObject({ data: 'data', loading: false }));
    expect(fetch).toHaveBeenCalledWith(expect.any(AbortSignal));
  });

  it('reports the error message, or the fallback for non-Error rejections', async () => {
    const { result } = setup(vi.fn().mockRejectedValue(new Error('network failure')));
    await waitFor(() => expect(result.current.error).toBe('network failure'));
    expect(result.current.loading).toBe(false);

    const fallback = setup(vi.fn().mockRejectedValue('string-rejection'));
    await waitFor(() => expect(fallback.result.current.error).toBe('fallback error'));
  });

  it('keeps the previous result when a refresh fails', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce('previous result')
      .mockRejectedValueOnce(new Error('network failure'));
    const { result } = setup(fetch);
    await waitFor(() => expect(result.current.data).toBe('previous result'));
    act(() => result.current.refresh());
    await waitFor(() => expect(result.current.error).toBe('network failure'));
    expect(result.current.data).toBe('previous result');
  });

  it('reports loading while a refresh is in flight', async () => {
    let resolveRefresh!: (value: string) => void;
    const fetch = vi.fn()
      .mockResolvedValueOnce('initial result')
      .mockImplementationOnce(() => new Promise<string>((resolve) => { resolveRefresh = resolve; }));
    const { result } = setup(fetch);
    await waitFor(() => expect(result.current.loading).toBe(false));
    act(() => result.current.refresh());
    await waitFor(() => expect(result.current.loading).toBe(true));
    act(() => resolveRefresh('refreshed result'));
    await waitFor(() => expect(result.current).toMatchObject({ data: 'refreshed result', loading: false }));
  });

  it('resets to loading when the session changes', async () => {
    const fetch = vi.fn().mockResolvedValueOnce('first').mockReturnValueOnce(new Promise(() => {}));
    const { result, rerender } = setup(fetch);
    await waitFor(() => expect(result.current.data).toBe('first'));
    rerender({ id: 'session-2' });
    expect(result.current).toMatchObject({ data: null, loading: true });
  });

  it('coalesces a burst of dirty ticks into one refetch', async () => {
    const fetch = vi.fn().mockResolvedValue('data');
    const { result, rerender } = setup(fetch, { id: 'session-1', dirtyTick: 0 });
    await waitFor(() => expect(result.current.data).toBe('data'));
    vi.useFakeTimers();
    for (let tick = 1; tick <= 5; tick++) rerender({ id: 'session-1', dirtyTick: tick });
    expect(fetch).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(DIRTY_REFRESH_MS); });
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('keeps data and defers dirty work while hidden, then refreshes on return', async () => {
    const fetch = vi.fn().mockResolvedValue('diff');
    const { result, rerender } = setup(fetch, { id: 'session-1', dirtyTick: 0 });
    await waitFor(() => expect(result.current.data).toBe('diff'));
    vi.useFakeTimers();
    act(() => setHidden(true));
    rerender({ id: 'session-1', dirtyTick: 1 });
    act(() => result.current.refresh());
    await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(result.current.data).toBe('diff');
    await act(async () => { setHidden(false); await vi.advanceTimersByTimeAsync(0); });
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(result.current.data).toBe('diff');
  });
});
