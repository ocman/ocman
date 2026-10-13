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
  runInNewContext(readFileSync(new URL('../../public/sw.js', import.meta.url), 'utf8'), {
    URL, self: { location: { origin: 'https://ocman.test' }, registration: { showNotification }, clients: { openWindow, matchAll },
      addEventListener: (kind: string, cb: (event: unknown) => void) => { listeners[kind] = cb; } },
  });
  async function emit(kind: string, event: object) {
    let work: Promise<unknown> | undefined;
    listeners[kind]({ ...event, waitUntil: (promise: Promise<unknown>) => { work = promise; } });
    await work;
  }
  return { emit, showNotification, openWindow, matchAll };
}

it('displays background push and preserves the owner-qualified target', async () => {
  const w = worker();
  await w.emit('push', { data: { json: () => ({ title: 'ocman — input required', body: 'A session needs input.', kind: 'prompt', tag: 'request', url: '/session/s?platform=r-owner%3Aopencode' }) } });
  expect(w.showNotification).toHaveBeenCalledWith('ocman — input required', expect.objectContaining({ requireInteraction: true, data: { url: '/session/s?platform=r-owner%3Aopencode' } }));
});

it('shows a safe generic alert for missing or malformed push data', async () => {
  const w = worker();
  await w.emit('push', { data: { json: () => { throw new Error('invalid JSON'); } } });
  expect(w.showNotification).toHaveBeenCalledWith('ocman', expect.objectContaining({ data: { url: '/inbox' } }));
  await w.emit('push', { data: { json: () => ({ url: 'https://evil.test/steal' }) } });
  expect(w.showNotification).toHaveBeenLastCalledWith('ocman', expect.objectContaining({ data: { url: '/inbox' } }));
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
