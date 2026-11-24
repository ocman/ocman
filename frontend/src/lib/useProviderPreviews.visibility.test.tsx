// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { useProviderPreviews } from './useProviderPreviews';
import { loadPreviewConfig, PreviewOwnerContext, resolvePreviews } from './previews';
import { APIError } from './api';
import { PR_CHECKS_REFRESH_EVENT } from './prChecksCache';

vi.mock('./previews', async (original) => ({
  ...await original<typeof import('./previews')>(),
  loadPreviewConfig: vi.fn().mockResolvedValue({ providers: [] }),
  previewCandidates: () => ['https://forge.example/repo/pulls/1'],
  resolvePreviews: vi.fn().mockResolvedValue([]),
}));
beforeEach(() => vi.clearAllMocks());
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('probes only ocman while the owner is offline, then retries the preview once', async () => {
  vi.useFakeTimers();
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
  let connected = false;
  const health = vi.fn(async () => new Response(JSON.stringify({ connected })));
  vi.stubGlobal('fetch', health);
  vi.mocked(resolvePreviews).mockClear().mockResolvedValue([])
    .mockRejectedValueOnce(new APIError('owner unavailable\n', 503));
  renderHook(() => useProviderPreviews('https://forge.example/repo/pulls/1'), {
    wrapper: ({ children }) => <PreviewOwnerContext.Provider value="box">{children}</PreviewOwnerContext.Provider>,
  });
  await act(async () => vi.advanceTimersByTimeAsync(1000));
  await act(async () => vi.advanceTimersByTimeAsync(30_000));
  expect(health).toHaveBeenCalledWith('/api/previews/availability?remoteId=box', expect.anything());
  expect(resolvePreviews).toHaveBeenCalledTimes(1);
  connected = true;
  await act(async () => vi.advanceTimersByTimeAsync(16_000));
  await act(async () => vi.advanceTimersByTimeAsync(1000));
  expect(resolvePreviews).toHaveBeenCalledTimes(2);
  expect(resolvePreviews).toHaveBeenLastCalledWith(expect.any(String), 'box', expect.any(AbortSignal), false);
  const probes = health.mock.calls.length;
  await act(async () => vi.advanceTimersByTimeAsync(30_000));
  expect(health).toHaveBeenCalledTimes(probes);
  expect(resolvePreviews).toHaveBeenCalledTimes(2);
});

it('resolves no previews while hidden, then resolves once on return', async () => {
  vi.useFakeTimers();
  let hidden = true;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  renderHook(() => useProviderPreviews('https://forge.example/repo/pulls/1'));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(loadPreviewConfig).not.toHaveBeenCalled();
  expect(resolvePreviews).not.toHaveBeenCalled();
  act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(resolvePreviews).toHaveBeenCalledTimes(1);
});

it('keeps an explicit cache bypass after its request is aborted by hiding', async () => {
  vi.useFakeTimers();
  let hidden = false;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  vi.mocked(resolvePreviews).mockClear().mockResolvedValue([])
    .mockImplementationOnce(async () => [])
    .mockImplementationOnce(() => new Promise(() => {}));
  renderHook(() => useProviderPreviews('https://forge.example/repo/pulls/1'));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  act(() => window.dispatchEvent(new CustomEvent(PR_CHECKS_REFRESH_EVENT)));
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
  expect(vi.mocked(resolvePreviews).mock.calls[1][2]?.aborted).toBe(true);
  act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(resolvePreviews).toHaveBeenCalledTimes(3);
  expect(resolvePreviews).toHaveBeenLastCalledWith(expect.any(String), 'local', expect.any(AbortSignal), true);
});
