// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { usePRChecks } from './usePRChecks';
import type { PRChecks } from './upstreamApi';

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

it('pauses pending CI checks while hidden and refreshes once on return', async () => {
  vi.useFakeTimers();
  let hidden = true;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  const fetchChecks = vi.fn().mockResolvedValue({ state: 'pending', checks: [] } as PRChecks);
  const { rerender } = renderHook(({ visible }) => usePRChecks('visibility/repo@sha', 'visibility-request', visible, fetchChecks), { initialProps: { visible: true } });
  await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
  expect(fetchChecks).not.toHaveBeenCalled();
  await act(async () => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
  expect(fetchChecks).toHaveBeenCalledTimes(1);
  await act(async () => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
  await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
  expect(fetchChecks).toHaveBeenCalledTimes(1);
  await act(async () => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
  expect(fetchChecks).toHaveBeenCalledTimes(2);
  rerender({ visible: false });
  await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
  expect(fetchChecks).toHaveBeenCalledTimes(2);
});
