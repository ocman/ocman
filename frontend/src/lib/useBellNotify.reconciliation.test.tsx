// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useBellNotify } from './useBellNotify';
import { __resetForTests, NOTIFY_RECHECK_DELAY_MS, notifyQueryKey, recheckNotifyData, type NotifySnapshot } from './useNotifyData';
import { queryClient } from './queryClient';
import { useUiStore } from './uiStore';
import { api } from './api';

vi.mock('./api', () => ({ api: { sessionsNotify: vi.fn() } }));

afterEach(() => {
  __resetForTests();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it('does not ring in a hidden tab when a stale busy row loses its resolved child prompt', async () => {
  vi.useFakeTimers();
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
  const audio = vi.fn(function () { throw new Error('unexpected bell'); });
  vi.stubGlobal('AudioContext', audio);
  useUiStore.setState({ bellEnabled: true });
  queryClient.setQueryData<NotifySnapshot>(notifyQueryKey, { entries: [], fetchedAt: 0 });
  let release!: () => void;
  const prompt = { platform: 'opencode', sessionId: 'child', requestId: 'p1' };
  vi.mocked(api.sessionsNotify).mockImplementationOnce(() => new Promise((resolve) => {
    release = () => resolve([{ id: 'parent', status: 'busy', seen: false,
      pendingPermission: true, permissions: [prompt] }]);
  }));
  const { unmount } = renderHook(() => useBellNotify());
  act(() => recheckNotifyData());
  await act(() => vi.advanceTimersByTimeAsync(NOTIFY_RECHECK_DELAY_MS));
  act(() => recheckNotifyData({ ...prompt, kind: 'permission' }));
  await act(async () => { release(); await vi.advanceTimersByTimeAsync(0); });
  expect(queryClient.getQueryData<NotifySnapshot>(notifyQueryKey)?.entries).toEqual([]);
  expect(audio).not.toHaveBeenCalled();
  unmount();
});
