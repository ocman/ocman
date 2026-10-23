// @vitest-environment jsdom
import type { ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook as renderHookRaw, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import * as api from './upstreamApi';
import { useForgeUser } from './useForgeUser';
import { renderHookWithQuery as renderHook } from '../test/renderWithQuery';

beforeEach(() => {
  vi.restoreAllMocks();
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

it('reuses a known identity for a minute, then rechecks', async () => {
  let now = 1_000;
  vi.spyOn(Date, 'now').mockImplementation(() => now);
  const fetcher = vi.spyOn(api, 'fetchForgeUser')
    .mockResolvedValueOnce({ login: 'alice', host: 'github.com' })
    .mockResolvedValueOnce({ login: 'bob', host: 'github.com' });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;

  const first = renderHookRaw(() => useForgeUser('/repo', 'origin', 'local'), { wrapper });
  await waitFor(() => expect(first.result.current.login).toBe('alice'));
  first.unmount();

  const cached = renderHookRaw(() => useForgeUser('/repo', 'origin', 'local'), { wrapper });
  expect(cached.result.current).toMatchObject({ login: 'alice', ready: true });
  cached.unmount();
  now += 60_001;

  const second = renderHookRaw(() => useForgeUser('/repo', 'origin', 'local'), { wrapper });
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
