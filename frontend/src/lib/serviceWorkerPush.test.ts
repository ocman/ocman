// @ts-expect-error Node-only test helper; application types exclude Node.
import { readFileSync } from 'node:fs';
// @ts-expect-error Node-only test helper; application types exclude Node.
import { runInNewContext } from 'node:vm';
import { expect, it, vi } from 'vitest';

function worker() {
  const listeners: Record<string, (event: unknown) => void> = {};
  const showNotification = vi.fn().mockResolvedValue(undefined);
  const openWindow = vi.fn().mockResolvedValue(undefined);
  const matchAll = vi.fn().mockResolvedValue([]);
  const setAppBadge = vi.fn().mockResolvedValue(undefined);
  const clearAppBadge = vi.fn().mockResolvedValue(undefined);
  runInNewContext(readFileSync(new URL('../../public/sw.js', import.meta.url), 'utf8'), {
    URL, navigator: { setAppBadge, clearAppBadge }, self: { location: { origin: 'https://ocman.test' }, registration: { showNotification }, clients: { openWindow, matchAll },
      addEventListener: (kind: string, cb: (event: unknown) => void) => { listeners[kind] = cb; } },
  });
  async function emit(kind: string, event: object) {
    let work: Promise<unknown> | undefined;
    listeners[kind]({ ...event, waitUntil: (promise: Promise<unknown>) => { work = promise; } });
    await work;
  }
  return { emit, showNotification, openWindow, matchAll, setAppBadge, clearAppBadge };
}

it('displays background push and preserves the owner-qualified target', async () => {
  const w = worker();
  await w.emit('push', { data: { json: () => ({ title: 'ocman — input required', body: 'A session needs input.', kind: 'prompt', tag: 'request', url: '/session/s?platform=r-owner%3Aopencode' }) } });
  expect(w.showNotification).toHaveBeenCalledWith('ocman — input required', expect.objectContaining({ requireInteraction: true, data: expect.objectContaining({ url: '/session/s?platform=r-owner%3Aopencode' }) }));
});

it('updates the installed icon badge from push and clears a zero count', async () => {
  const w = worker();
  await w.emit('push', { data: { json: () => ({ badgeCount: 3 }) } });
  expect(w.setAppBadge).toHaveBeenCalledWith(3);
  await w.emit('push', { data: { json: () => ({ badgeCount: 0 }) } });
  expect(w.clearAppBadge).toHaveBeenCalledOnce();
  await w.emit('push', { data: { json: () => ({ badgeCount: -1 }) } });
  expect(w.setAppBadge).toHaveBeenCalledOnce();
});

it('still displays notifications if OS badging is rejected', async () => {
  const w = worker(); w.setAppBadge.mockRejectedValue(new Error('denied'));
  await w.emit('push', { data: { json: () => ({ badgeCount: 1 }) } });
  expect(w.showNotification).toHaveBeenCalledOnce();
});

it('makes required push notifications silent and non-sticky while an app window is active', async () => {
  const w = worker();
  w.matchAll.mockResolvedValue([
    { focused: false, visibilityState: 'visible' },
    { focused: true, visibilityState: 'visible' },
  ]);
  await w.emit('push', { data: { json: () => ({ kind: 'prompt' }) } });
  expect(w.matchAll).toHaveBeenCalledWith({ type: 'window', includeUncontrolled: true });
  expect(w.showNotification).toHaveBeenCalledWith('Session update', expect.objectContaining({ silent: true, requireInteraction: false }));
});

it.each([
  { focused: false, visibilityState: 'visible' },
  { focused: true, visibilityState: 'hidden' },
])('keeps push alerts normal for an inactive window: %j', async (client) => {
  const w = worker();
  w.matchAll.mockResolvedValue([client]);
  await w.emit('push', { data: { json: () => ({ kind: 'prompt' }) } });
  expect(w.showNotification.mock.calls[0][1]).toMatchObject({ requireInteraction: true });
  expect(w.showNotification.mock.calls[0][1]).not.toHaveProperty('silent');
});

it('shows a safe generic alert for missing or malformed push data', async () => {
  const w = worker();
  await w.emit('push', { data: { json: () => { throw new Error('invalid JSON'); } } });
  expect(w.showNotification).toHaveBeenCalledWith('Session update', expect.objectContaining({ data: expect.objectContaining({ url: '/inbox' }) }));
  await w.emit('push', { data: { json: () => ({ url: 'https://evil.test/steal' }) } });
  expect(w.showNotification).toHaveBeenLastCalledWith('Session update', expect.objectContaining({ data: expect.objectContaining({ url: '/inbox' }) }));
});

it('opens a session when no app window exists', async () => {
  const w = worker(); const close = vi.fn();
  await w.emit('notificationclick', { notification: { close, data: { url: '/session/s?platform=remote' } } });
  expect(close).toHaveBeenCalledOnce(); expect(w.openWindow).toHaveBeenCalledWith('/session/s?platform=remote');
});

it('focuses an existing window and routes via the SPA', async () => {
  const w = worker(); const focus = vi.fn().mockResolvedValue(undefined); const postMessage = vi.fn();
  w.matchAll.mockResolvedValue([{ url: 'https://ocman.test/', focus, postMessage }]);
  await w.emit('notificationclick', { notification: { close: vi.fn(), data: { url: '/session/s' } } });
  expect(focus).toHaveBeenCalledOnce(); expect(postMessage).toHaveBeenCalledWith({ type: 'ocman:navigate', url: '/session/s' });
  expect(w.openWindow).not.toHaveBeenCalled();
});

it('rejects notification clicks targeting other origins', async () => {
  const w = worker();
  await w.emit('notificationclick', { notification: { close: vi.fn(), data: { url: 'https://evil.test/' } } });
  expect(w.matchAll).not.toHaveBeenCalled(); expect(w.openWindow).not.toHaveBeenCalled();
});
