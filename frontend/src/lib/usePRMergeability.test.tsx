// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { usePRMergeability } from './usePRMergeability';
import * as api from './upstreamApi';
import type { PR } from './upstreamApi';
import { clearPRChecksCache } from './prChecksCache';

const pr = { number: 42, status: 'open', headSha: 'abc', updatedAt: 'today', host: 'github.com', repo: 'a/repo' } as PR;

describe('usePRMergeability', () => {
  beforeEach(() => { vi.spyOn(api, 'fetchPRMergeability'); });
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it('fetches the owner-scoped endpoint with the cancellation signal', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ mergeable: false, approved: true }), { status: 200 }));
    vi.stubGlobal('fetch', fetch);
    const signal = new AbortController().signal;
    expect(await api.fetchPRMergeability({ dir: '/repo with spaces', remoteId: 'r-one', remote: 'origin', number: 42, signal })).toEqual({ mergeable: false, approved: true });
    const [url, options] = fetch.mock.calls[0];
    expect(url).toBe('/api/project/pr-mergeability?dir=%2Frepo+with+spaces&remoteId=r-one&remote=origin&number=42');
    expect(options.signal).toBe(signal);
  });

  it('fetches unknown mergeability only when visible, and clears stale results on a new head', async () => {
    vi.mocked(api.fetchPRMergeability).mockResolvedValueOnce({ mergeable: false, approved: true }).mockReturnValue(new Promise(() => {}));
    const { result, rerender, unmount } = renderHook(
      ({ visible, headSha }) => usePRMergeability({ ...pr, headSha }, '/repo', 'local', 'origin', visible),
      { initialProps: { visible: false, headSha: 'abc' } },
    );
    expect(api.fetchPRMergeability).not.toHaveBeenCalled();
    rerender({ visible: true, headSha: 'abc' });
    await waitFor(() => expect(result.current).toEqual({ mergeable: false, approved: true }));
    expect(api.fetchPRMergeability).toHaveBeenCalledWith(expect.objectContaining({ number: 42, remoteId: 'local' }));
    rerender({ visible: true, headSha: 'def' });
    expect(result.current).toEqual({ mergeable: undefined, approved: undefined });
    const signal = vi.mocked(api.fetchPRMergeability).mock.calls[1][0].signal;
    unmount();
    expect(signal.aborted).toBe(true);
  });

  it.each([true, false])('fetches approval even with known mergeability %s', async (mergeable) => {
    vi.mocked(api.fetchPRMergeability).mockResolvedValue({ mergeable: null, approved: true });
    const { result } = renderHook(() => usePRMergeability({ ...pr, mergeable }, '/repo', 'local', 'origin', true));
    expect(result.current.mergeable).toBe(mergeable);
    await waitFor(() => expect(result.current.approved).toBe(true));
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(1);
  });

  it('revalidates on a refresh for its repository, but not another repository', async () => {
    vi.mocked(api.fetchPRMergeability).mockResolvedValueOnce({ mergeable: true, approved: true }).mockResolvedValueOnce({ mergeable: false, approved: false });
    const { result } = renderHook(() => usePRMergeability(pr, '/repo', 'local', 'origin', true));
    await waitFor(() => expect(result.current).toEqual({ mergeable: true, approved: true }));
    act(() => clearPRChecksCache(['github.com/other/repo']));
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(1);
    act(() => clearPRChecksCache(['github.com/a/repo']));
    await waitFor(() => expect(result.current).toEqual({ mergeable: false, approved: false }));
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(2);
  });

  it.each(['draft', 'closed', 'merged'] as const)('fetches approval once for %s PRs without waiting for mergeability', async (status) => {
    vi.mocked(api.fetchPRMergeability).mockResolvedValue({ mergeable: null, approved: true });
    const { result } = renderHook(() => usePRMergeability({ ...pr, status }, '/repo', 'local', 'origin', true));
    await waitFor(() => expect(result.current.approved).toBe(true));
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(1);
  });

  it('retries errors and pending computations, and stops when hidden', async () => {
    vi.useFakeTimers();
    vi.mocked(api.fetchPRMergeability).mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ mergeable: null, approved: null }).mockResolvedValueOnce({ mergeable: true, approved: false });
    const { result, rerender } = renderHook(({ visible }) => usePRMergeability(pr, '/repo', 'local', 'origin', visible), { initialProps: { visible: true } });
    await act(async () => {});
    expect(result.current).toEqual({ mergeable: undefined, approved: undefined });
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(result.current).toEqual({ mergeable: null, approved: null });
    rerender({ visible: false });
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(2);
    rerender({ visible: true });
    await act(async () => {});
    expect(result.current).toEqual({ mergeable: true, approved: false });
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(3);
  });

  it('honors a reviews-only rate-limit deadline across viewport changes', async () => {
    vi.useFakeTimers();
    const retryAfter = new Date(Date.now() + 120_000).toISOString();
    vi.mocked(api.fetchPRMergeability).mockRejectedValueOnce(new api.UpstreamApiError({ error: { code: 'rate_limited', message: 'limited', retryAfter } }, 429))
      .mockResolvedValue({ mergeable: true, approved: true });
    const { result, rerender } = renderHook(({ visible }) => usePRMergeability({ ...pr, mergeable: true }, '/repo', 'local', 'origin', visible), { initialProps: { visible: true } });
    await act(async () => {});
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    rerender({ visible: false });
    rerender({ visible: true });
    await act(async () => { await vi.advanceTimersByTimeAsync(89_999); });
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(1);
    expect(result.current.mergeable).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(2);
    expect(result.current.approved).toBe(true);
  });

  it.each([401, 403, 404])('stops permanent upstream failures %s until explicit refresh', async (upstreamStatus) => {
    vi.useFakeTimers();
    vi.mocked(api.fetchPRMergeability).mockRejectedValue(new api.UpstreamApiError({ error: { code: 'upstream_status', message: 'failed', upstreamStatus } }, 502));
    const { rerender } = renderHook(({ visible }) => usePRMergeability(pr, '/repo', 'local', 'origin', visible), { initialProps: { visible: true } });
    await act(async () => {});
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    rerender({ visible: false });
    rerender({ visible: true });
    await act(async () => {});
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(1);
    act(() => clearPRChecksCache(['github.com/a/repo']));
    await act(async () => {});
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(2);
  });

  it('backs off transient failures from 15 seconds to a 60-second ceiling', async () => {
    vi.useFakeTimers();
    vi.mocked(api.fetchPRMergeability).mockRejectedValue(new Error('offline'));
    renderHook(() => usePRMergeability(pr, '/repo', 'local', 'origin', true));
    await act(async () => {});
    for (const [delay, calls] of [[15_000, 2], [30_000, 3], [60_000, 4], [60_000, 5]]) {
      await act(async () => { await vi.advanceTimersByTimeAsync(delay - 1); });
      expect(api.fetchPRMergeability).toHaveBeenCalledTimes(calls - 1);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(api.fetchPRMergeability).toHaveBeenCalledTimes(calls);
    }
  });
});
