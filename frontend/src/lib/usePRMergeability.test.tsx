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
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ mergeable: false }), { status: 200 }));
    vi.stubGlobal('fetch', fetch);
    const signal = new AbortController().signal;
    expect(await api.fetchPRMergeability({ dir: '/repo with spaces', remoteId: 'r-one', remote: 'origin', number: 42, signal })).toBe(false);
    const [url, options] = fetch.mock.calls[0];
    expect(url).toBe('/api/project/pr-mergeability?dir=%2Frepo+with+spaces&remoteId=r-one&remote=origin&number=42');
    expect(options.signal).toBe(signal);
  });

  it('fetches unknown mergeability only when visible, and clears stale results on a new head', async () => {
    vi.mocked(api.fetchPRMergeability).mockResolvedValueOnce(false).mockReturnValue(new Promise(() => {}));
    const { result, rerender, unmount } = renderHook(
      ({ visible, headSha }) => usePRMergeability({ ...pr, headSha }, '/repo', 'local', 'origin', visible),
      { initialProps: { visible: false, headSha: 'abc' } },
    );
    expect(api.fetchPRMergeability).not.toHaveBeenCalled();
    rerender({ visible: true, headSha: 'abc' });
    await waitFor(() => expect(result.current).toBe(false));
    expect(api.fetchPRMergeability).toHaveBeenCalledWith(expect.objectContaining({ number: 42, remoteId: 'local' }));
    rerender({ visible: true, headSha: 'def' });
    expect(result.current).toBeUndefined();
    const signal = vi.mocked(api.fetchPRMergeability).mock.calls[1][0].signal;
    unmount();
    expect(signal.aborted).toBe(true);
  });

  it.each([true, false])('uses a known value %s without fetching', (mergeable) => {
    const { result } = renderHook(() => usePRMergeability({ ...pr, mergeable }, '/repo', 'local', 'origin', true));
    expect(result.current).toBe(mergeable);
    expect(api.fetchPRMergeability).not.toHaveBeenCalled();
  });

  it('revalidates on a refresh for its repository, but not another repository', async () => {
    vi.mocked(api.fetchPRMergeability).mockResolvedValueOnce(true).mockResolvedValueOnce(false);
    const { result } = renderHook(() => usePRMergeability(pr, '/repo', 'local', 'origin', true));
    await waitFor(() => expect(result.current).toBe(true));
    act(() => clearPRChecksCache(['github.com/other/repo']));
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(1);
    act(() => clearPRChecksCache(['github.com/a/repo']));
    await waitFor(() => expect(result.current).toBe(false));
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(2);
  });

  it.each(['draft', 'closed', 'merged'] as const)('does not fetch %s PRs', (status) => {
    renderHook(() => usePRMergeability({ ...pr, status }, '/repo', 'local', 'origin', true));
    expect(api.fetchPRMergeability).not.toHaveBeenCalled();
  });

  it('retries errors and pending computations, and stops when hidden', async () => {
    vi.useFakeTimers();
    vi.mocked(api.fetchPRMergeability).mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(null).mockResolvedValueOnce(true);
    const { result, rerender } = renderHook(({ visible }) => usePRMergeability(pr, '/repo', 'local', 'origin', visible), { initialProps: { visible: true } });
    await act(async () => {});
    expect(result.current).toBeUndefined();
    await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
    expect(result.current).toBeUndefined();
    rerender({ visible: false });
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(2);
    rerender({ visible: true });
    await act(async () => {});
    expect(result.current).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(api.fetchPRMergeability).toHaveBeenCalledTimes(3);
  });
});
