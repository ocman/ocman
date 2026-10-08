// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useProviderPreviews } from './useProviderPreviews';
import { loadPreviewConfig, resolvePreviews } from './previews';

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
