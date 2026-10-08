// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useProviderPreviews } from './useProviderPreviews';
import { loadPreviewConfig, resolvePreviews } from './previews';
import { PR_CHECKS_REFRESH_EVENT } from './prChecksCache';

vi.mock('./previews', async (original) => ({
  ...await original<typeof import('./previews')>(),
  loadPreviewConfig: vi.fn().mockResolvedValue({ providers: [] }),
  mayPreview: () => true,
  resolvePreviews: vi.fn().mockResolvedValue([]),
}));
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

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
