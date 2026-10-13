// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useNotificationNotify, __resetForTests } from './useNotificationNotify';
import { useUiStore } from './uiStore';

vi.mock('./useNotifyBaseline', () => ({
  notifyStateKey: () => 'prompt',
  useNotifyBaseline: (consume: (rows: unknown[], baseline: Map<string, string>) => void) => {
    consume([{ id: 'mobile', pendingQuestion: true, status: 'waiting' }], new Map());
  },
}));

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); __resetForTests(); useUiStore.setState({ notificationsEnabled: false }); });

it('uses the service worker on mobile instead of silently losing a notification', async () => {
  const showNotification = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal('Notification', Object.assign(function () { throw new TypeError('Use showNotification'); }, { permission: 'granted' }));
  vi.stubGlobal('navigator', { serviceWorker: {
    getRegistration: vi.fn().mockResolvedValue({ showNotification, pushManager: { getSubscription: vi.fn().mockResolvedValue(null) } }),
  } });
  useUiStore.setState({ notificationsEnabled: true });
  renderHook(() => useNotificationNotify());
  await waitFor(() => expect(showNotification).toHaveBeenCalledWith('ocman — input required', expect.objectContaining({ data: expect.objectContaining({ url: '/session/mobile' }) })));
});

it('does not duplicate backend alerts on push-subscribed devices', async () => {
  const showNotification = vi.fn();
  vi.stubGlobal('Notification', { permission: 'granted' });
  vi.stubGlobal('navigator', { serviceWorker: {
    getRegistration: vi.fn().mockResolvedValue({ showNotification, pushManager: { getSubscription: vi.fn().mockResolvedValue({ endpoint: 'device' }) } }),
  } });
  useUiStore.setState({ notificationsEnabled: true });
  renderHook(() => useNotificationNotify());
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(showNotification).not.toHaveBeenCalled();
});
