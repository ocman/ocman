import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { disablePush, enablePush, pushSubscribed, pushSupported, showSystemNotification } from './pushNotifications';
import { fetchJSON, postJSON } from './api.requests';

vi.mock('./api.requests', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));

function setup(existing = false) {
  const subscription = { endpoint: 'https://fcm.googleapis.com/device', options: {}, toJSON: () => ({ endpoint: 'https://fcm.googleapis.com/device' }), unsubscribe: vi.fn().mockResolvedValue(true) };
  const registration = {
    pushManager: { getSubscription: vi.fn().mockResolvedValue(existing ? subscription : null), subscribe: vi.fn().mockResolvedValue(subscription) },
    showNotification: vi.fn().mockResolvedValue(undefined),
  };
  vi.stubGlobal('navigator', { serviceWorker: {
    getRegistration: vi.fn().mockResolvedValue(registration), register: vi.fn().mockResolvedValue(registration), ready: Promise.resolve(registration),
  } });
  vi.stubGlobal('PushManager', function () {});
  return { subscription, registration };
}

beforeEach(() => {
  vi.mocked(fetchJSON).mockResolvedValue({ publicKey: 'AQID' });
  vi.mocked(postJSON).mockResolvedValue(undefined);
});
afterEach(() => { vi.unstubAllGlobals(); vi.resetAllMocks(); });

describe('device push subscriptions', () => {
  it('subscribes with the server key and persists the device', async () => {
    const { registration } = setup();
    expect(pushSupported()).toBe(true);
    expect(await pushSubscribed()).toBe(false);
    await enablePush();
    expect(registration.pushManager.subscribe).toHaveBeenCalledWith({ userVisibleOnly: true, applicationServerKey: new Uint8Array([1, 2, 3]) });
    expect(postJSON).toHaveBeenCalledWith('/api/notifications/subscribe', { endpoint: 'https://fcm.googleapis.com/device' }, { parseJSON: false });
  });
  it('refreshes an existing subscription without creating another', async () => {
    const { registration } = setup(true);
    expect(await pushSubscribed()).toBe(true);
    await enablePush();
    expect(registration.pushManager.subscribe).not.toHaveBeenCalled();
    expect(postJSON).toHaveBeenCalledOnce();
  });
  it('removes a newly created browser subscription if server persistence fails', async () => {
    const { subscription } = setup();
    vi.mocked(postJSON).mockRejectedValue(new Error('offline'));
    await expect(enablePush()).rejects.toThrow('offline');
    expect(subscription.unsubscribe).toHaveBeenCalledOnce();
  });
  it('keeps an existing subscription on a failed refresh', async () => {
    const { subscription } = setup(true);
    vi.mocked(postJSON).mockRejectedValue(new Error('offline'));
    await expect(enablePush()).rejects.toThrow('offline');
    expect(subscription.unsubscribe).not.toHaveBeenCalled();
  });
  it('rotates browser subscriptions when the installation key changes', async () => {
    const { subscription, registration } = setup(true);
    Object.assign(subscription.options, { applicationServerKey: new Uint8Array([9, 9]).buffer });
    await enablePush();
    expect(subscription.unsubscribe).toHaveBeenCalledOnce();
    expect(registration.pushManager.subscribe).toHaveBeenCalledOnce();
  });
  it('deletes the server subscription before unsubscribing the browser', async () => {
    const { subscription } = setup(true);
    await disablePush();
    expect(postJSON).toHaveBeenCalledWith('/api/notifications/unsubscribe', { endpoint: subscription.endpoint }, { parseJSON: false });
    expect(subscription.unsubscribe).toHaveBeenCalledOnce();
  });
  it('does not hide failed disablement', async () => {
    const { subscription } = setup(true);
    vi.mocked(postJSON).mockRejectedValue(new Error('offline'));
    await expect(disablePush()).rejects.toThrow('offline');
    expect(subscription.unsubscribe).not.toHaveBeenCalled();
  });
  it('reports browser unsubscribe failures', async () => {
    const { subscription } = setup(true);
    subscription.unsubscribe.mockResolvedValue(false);
    await expect(disablePush()).rejects.toThrow('Could not unsubscribe');
  });
  it('handles unsupported browsers and devices without a subscription', async () => {
    setup(); await disablePush(); expect(postJSON).not.toHaveBeenCalled();
    vi.stubGlobal('navigator', {}); vi.stubGlobal('PushManager', undefined);
    expect(pushSupported()).toBe(false); expect(await pushSubscribed()).toBe(false);
    await disablePush();
  });
});

describe('OS notification display', () => {
  it('uses the service worker when registered', async () => {
    const { registration } = setup();
    const Constructor = vi.fn(); Object.assign(Constructor, { permission: 'granted' }); vi.stubGlobal('Notification', Constructor);
    await showSystemNotification('alert', { data: { url: '/session/s' } });
    expect(registration.showNotification).toHaveBeenCalledWith('alert', { data: { url: '/session/s' } });
    expect(Constructor).not.toHaveBeenCalled();
  });
  it('falls back to the desktop constructor and invokes click routing', async () => {
    const { registration } = setup();
    vi.stubGlobal('navigator', {});
    const notification = { onclick: undefined as undefined | (() => void), close: vi.fn() };
    const Constructor = vi.fn(function () { return notification; }); Object.assign(Constructor, { permission: 'granted' }); vi.stubGlobal('Notification', Constructor);
    const click = vi.fn(); const focus = vi.fn(); vi.stubGlobal('window', { focus });
    await showSystemNotification('alert', {}, click);
    notification.onclick?.();
    expect(click).toHaveBeenCalledOnce(); expect(focus).toHaveBeenCalledOnce(); expect(notification.close).toHaveBeenCalledOnce();
    expect(registration.showNotification).not.toHaveBeenCalled();
  });
  it('does not display without permission', async () => {
    const { registration } = setup();
    vi.stubGlobal('Notification', { permission: 'denied' }); await showSystemNotification('alert', {});
    vi.stubGlobal('Notification', undefined); await showSystemNotification('alert', {});
    expect(registration.showNotification).not.toHaveBeenCalled();
  });
});
