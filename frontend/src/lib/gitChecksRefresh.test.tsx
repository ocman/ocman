// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { cachePRChecks, clearPRChecksCache } from './prChecksCache';
import { usePRChecks } from './usePRChecks';
import { useProviderPreviews } from './useProviderPreviews';
import { resolvePreviews } from './previews';
import type { PRChecks } from './upstreamApi';

vi.mock('./previews', async (importOriginal) => ({
  ...await importOriginal<typeof import('./previews')>(),
  loadPreviewConfig: vi.fn().mockResolvedValue(null),
  resolvePreviews: vi.fn().mockResolvedValue([]),
}));

it('scoped hints refresh matching checks without resetting unrelated checks or rich previews', async () => {
  clearPRChecksCache();
  const checks: PRChecks = { state: 'success', checks: [{ name: 'build', state: 'success' }] };
  cachePRChecks('github.com/a/repo@sha', checks);
  cachePRChecks('github.com/other/repo@sha', checks);
  const matching = vi.fn().mockResolvedValue(checks);
  const unrelated = vi.fn().mockResolvedValue(checks);
  const { unmount } = renderHook(() => {
    usePRChecks('github.com/a/repo@sha', 'a', true, matching);
    usePRChecks('github.com/other/repo@sha', 'other', true, unrelated);
    useProviderPreviews('https://github.com/other/repo/pull/1');
  });
  await waitFor(() => expect(resolvePreviews).toHaveBeenCalledOnce());
  act(() => clearPRChecksCache(['github.com/a/repo']));
  await waitFor(() => expect(matching).toHaveBeenCalledOnce());
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 400)); });
  expect(unrelated).not.toHaveBeenCalled();
  expect(resolvePreviews).toHaveBeenCalledOnce();
  act(() => clearPRChecksCache());
  await waitFor(() => expect(unrelated).toHaveBeenCalledOnce());
  await waitFor(() => expect(resolvePreviews).toHaveBeenCalledTimes(2));
  unmount();
});
