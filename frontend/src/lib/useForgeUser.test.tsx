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

it('rechecks forge identity after the cache expires', async () => {
  let now = 1_000;
  vi.spyOn(Date, 'now').mockImplementation(() => now);
  const fetcher = vi.spyOn(api, 'fetchForgeUser')
    .mockResolvedValueOnce({ login: 'alice', host: 'github.com' })
    .mockResolvedValueOnce({ login: 'bob', host: 'github.com' });

  const first = renderHook(() => useForgeUser('/repo', 'origin', 'local'));
  await waitFor(() => expect(first.result.current.login).toBe('alice'));
  first.unmount();
  now += 60_001;

  const second = renderHook(() => useForgeUser('/repo', 'origin', 'local'));
  await waitFor(() => expect(second.result.current.login).toBe('bob'));
  expect(fetcher).toHaveBeenCalledTimes(2);
});
