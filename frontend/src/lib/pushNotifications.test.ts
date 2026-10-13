import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { disablePush, enablePush, pushSubscribed, pushSupported, showSystemNotification, dismissIrrelevantSessionNotifications } from './pushNotifications';
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
  vi.mocked(postJSON).mockImplementation(async (url) => url.endsWith('/subscription') ? { active: true } : undefined);
});
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.resetAllMocks(); });

describe('device push subscriptions', () => {
  it('times out stalled readiness without subscribing or persisting', async () => {
    vi.useFakeTimers();
    const { registration } = setup();
    Object.assign(navigator.serviceWorker, { ready: new Promise(() => {}) });
    const rejected = expect(enablePush()).rejects.toThrow('Service worker did not become ready');
    await vi.advanceTimersByTimeAsync(10_000);
    await rejected;
    expect(registration.pushManager.subscribe).not.toHaveBeenCalled();
    expect(postJSON).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });
  it('clears the readiness timer after success', async () => {
    vi.useFakeTimers();
    setup();
    await enablePush();
    expect(vi.getTimerCount()).toBe(0);
  });
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
    vi.mocked(postJSON).mockClear();
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
  it('reconciles browser-only subscriptions against server registration on reload', async () => {
    setup(true);
    vi.mocked(postJSON).mockResolvedValue({ active: false });
    expect(await pushSubscribed()).toBe(false);
    vi.mocked(postJSON).mockRejectedValue(new Error('offline'));
    expect(await pushSubscribed()).toBe(false);
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
    expect(registration.showNotification).toHaveBeenCalledWith('alert', expect.objectContaining({ data: expect.objectContaining({ url: '/session/s' }) }));
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

it('dismisses read/resolved session alerts while retaining current and newer notifications', async () => {
  const stale = { tag: 'ocman:opencode:read:completed', data: { createdAt: 10 }, close: vi.fn() };
  const resolved = { tag: 'ocman:opencode:answered:prompt', data: { createdAt: 10 }, close: vi.fn() };
  const pending = { tag: 'ocman:opencode:pending:prompt', data: { createdAt: 10 }, close: vi.fn() };
  const unread = { tag: 'ocman:opencode:unread:completed', data: { createdAt: 10 }, close: vi.fn() };
  const future = { tag: 'ocman:opencode:new:prompt', data: { createdAt: 200 }, close: vi.fn() };
  const factory = { tag: 'ocman:local:factory-action', data: { createdAt: 10 }, close: vi.fn() };
  vi.stubGlobal('navigator', { serviceWorker: { getRegistration: vi.fn().mockResolvedValue({ getNotifications: vi.fn().mockResolvedValue([stale, resolved, pending, unread, future, factory]) }) } });
  await dismissIrrelevantSessionNotifications([
    { id: 'pending', platform: 'opencode', status: 'busy', seen: true, pendingQuestion: true },
    { id: 'unread', platform: 'opencode', status: 'waiting', seen: false },
  ], 100);
  expect(stale.close).toHaveBeenCalledOnce(); expect(resolved.close).toHaveBeenCalledOnce();
  for (const kept of [pending, unread, future, factory]) expect(kept.close).not.toHaveBeenCalled();
});

it('does not reconcile notifications against an obsolete snapshot', async () => {
  const close = vi.fn(); const controller = new AbortController();
  vi.stubGlobal('navigator', { serviceWorker: { getRegistration: vi.fn().mockResolvedValue({ getNotifications: async () => { controller.abort(); return [{ tag: 'ocman:session:completed', data: { createdAt: 10 }, close }]; } }) } });
  await dismissIrrelevantSessionNotifications([], 100, controller.signal);
  expect(close).not.toHaveBeenCalled();
});
