// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import * as api from './upstreamApi';
import { _resetForgeUserCacheForTests, useForgeUser } from './useForgeUser';

beforeEach(() => {
  vi.restoreAllMocks();
  _resetForgeUserCacheForTests();
});

it('cancels an in-flight identity request on unmount', async () => {
  const fetcher = vi.spyOn(api, 'fetchForgeUser').mockReturnValue(new Promise(() => {}));
  const { unmount } = renderHook(() => useForgeUser('/repo', 'origin', 'local'));
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  const signal = fetcher.mock.calls[0][0].signal;
  expect(signal?.aborted).toBe(false);

  unmount();
  expect(signal?.aborted).toBe(true);
});
