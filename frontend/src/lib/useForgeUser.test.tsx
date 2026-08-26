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

it('does not expose the previous owner identity while loading', async () => {
  const snapshots: Array<{ owner: string; login: string | null }> = [];
  vi.spyOn(api, 'fetchForgeUser')
    .mockResolvedValueOnce({ login: 'alice', host: 'github.com' })
    .mockReturnValueOnce(new Promise(() => {}));

  const { result, rerender } = renderHook(
    ({ owner }) => {
      const value = useForgeUser('/repo', 'origin', owner);
      snapshots.push({ owner, login: value.login });
      return value;
    },
    { initialProps: { owner: 'old-owner' } },
  );
  await waitFor(() => expect(result.current.login).toBe('alice'));

  rerender({ owner: 'new-owner' });
  expect(result.current.login).toBeNull();
  expect(snapshots).not.toContainEqual({ owner: 'new-owner', login: 'alice' });
});
