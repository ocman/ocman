// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { onSessionChanged, onSseConnect } from './useGlobalEvents';
import { useSessionLiveRefresh } from './useSessionLiveRefresh';

vi.mock('./useGlobalEvents', () => ({ onSessionChanged: vi.fn(), onSseConnect: vi.fn() }));

it('reconciles on reconnect, scopes changes, and removes subscriptions on owner change and unmount', async () => {
  const stopChanges = vi.fn();
  const stopConnect = vi.fn();
  vi.mocked(onSessionChanged).mockReturnValue(stopChanges);
  vi.mocked(onSseConnect).mockReturnValue(stopConnect);
  const refresh = vi.fn().mockResolvedValue(undefined);
  const { rerender, unmount } = renderHook(({ platform }) => useSessionLiveRefresh('s1', platform, refresh), {
    initialProps: { platform: 'r-box:fake' },
  });
  const changed = vi.mocked(onSessionChanged).mock.calls[0][0];
  act(() => {
    changed('s1', undefined, undefined, 'fake');
    changed('other', undefined, undefined, 'r-box:fake');
    changed('s1');
  });
  expect(refresh).not.toHaveBeenCalled();
  act(() => changed('s1', undefined, undefined, 'r-box:fake'));
  expect(refresh).toHaveBeenCalledTimes(1);
  act(() => vi.mocked(onSseConnect).mock.calls[0][0]());
  expect(refresh).toHaveBeenCalledTimes(2);
  // A failed reload does not escape as an unhandled rejection.
  refresh.mockRejectedValueOnce(new Error('offline'));
  await act(async () => changed('s1', undefined, undefined, 'r-box:fake'));
  rerender({ platform: 'fake' });
  expect(stopChanges).toHaveBeenCalledTimes(1);
  expect(stopConnect).toHaveBeenCalledTimes(1);
  unmount();
  expect(stopChanges).toHaveBeenCalledTimes(2);
  expect(stopConnect).toHaveBeenCalledTimes(2);
});
