// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { PreviewSessionContext, resolvePreviews } from './previews';
import { useProviderPreviews } from './useProviderPreviews';

vi.mock('./previews', async (original) => ({
  ...await original<typeof import('./previews')>(),
  loadPreviewConfig: vi.fn().mockResolvedValue(null),
  resolvePreviews: vi.fn().mockResolvedValue([{ provider: 'github', kind: 'pr', id: 'a/r#1', state: 'ok' }]),
}));
afterEach(() => { vi.useRealTimers(); vi.clearAllMocks(); });

it('refreshes unchanged conversation text when switching sessions without remounting', async () => {
  vi.useFakeTimers();
  let session = 'first';
  const { result, rerender } = renderHook(() => useProviderPreviews('https://github.com/a/r/pull/1'), {
    wrapper: ({ children }) => <PreviewSessionContext.Provider value={session}>{children}</PreviewSessionContext.Provider>,
  });
  await act(async () => vi.advanceTimersByTimeAsync(300));
  expect(result.current.previews).toHaveLength(1);
  expect(resolvePreviews).toHaveBeenCalledTimes(1);
  session = 'second';
  rerender();
  expect(result.current.previews).toHaveLength(0);
  await act(async () => vi.advanceTimersByTimeAsync(300));
  expect(resolvePreviews).toHaveBeenCalledTimes(2);
  expect(result.current.previews).toHaveLength(1);
  rerender();
  await act(async () => vi.advanceTimersByTimeAsync(300));
  expect(resolvePreviews).toHaveBeenCalledTimes(2);
});
