import { fetchJSON, postJSON } from './api.requests';
import type { NotifyEntry } from './api.types';

const pageNotifications = new Set<Notification>();

export function pushSupported() {
  return typeof navigator !== 'undefined' && 'serviceWorker' in navigator && typeof PushManager !== 'undefined';
}

export async function pushSubscribed(): Promise<boolean> {
  if (typeof navigator === 'undefined' || !navigator.serviceWorker) return false;
  const registration = await navigator.serviceWorker.getRegistration();
  const subscription = await registration?.pushManager?.getSubscription();
  if (!subscription) return false;
  try {
    const status = await postJSON<{ active: boolean }>('/api/notifications/subscription', { endpoint: subscription.endpoint });
    return status.active;
  } catch {
    // Suppress page alerts only with confirmed server registration.
    return false;
  }
}

export async function enablePush(): Promise<void> {
  const { publicKey } = await fetchJSON<{ publicKey: string }>('/api/notifications/push-key');
  await navigator.serviceWorker.register('/sw.js');
  let timeout: ReturnType<typeof setTimeout> | undefined;
  const registration = await Promise.race([
    navigator.serviceWorker.ready,
    new Promise<never>((_, reject) => { timeout = setTimeout(() => reject(new Error('Service worker did not become ready. Reload and retry.')), 10_000); }),
  ]).finally(() => clearTimeout(timeout));
  let subscription = await registration.pushManager.getSubscription();
  const key = Uint8Array.from(atob(publicKey.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0));
  if (subscription && subscription.options.applicationServerKey &&
      (subscription.options.applicationServerKey.byteLength !== key.length ||
      !key.every((byte, i) => byte === new Uint8Array(subscription!.options.applicationServerKey!)[i]))) {
    await subscription.unsubscribe();
    subscription = null;
  }
  const created = !subscription;
  subscription ??= await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
  try {
    await postJSON('/api/notifications/subscribe', subscription.toJSON(), { parseJSON: false });
  } catch (error) {
    if (created) await subscription.unsubscribe();
    throw error;
  }
}

export async function disablePush(): Promise<void> {
  if (typeof navigator === 'undefined' || !navigator.serviceWorker) return;
  const registration = await navigator.serviceWorker.getRegistration();
  const subscription = await registration?.pushManager?.getSubscription();
  if (!subscription) return;
  // Delete server-side first so a failed unsubscribe remains visibly retryable.
  await postJSON('/api/notifications/unsubscribe', { endpoint: subscription.endpoint }, { parseJSON: false });
  if (!await subscription.unsubscribe()) throw new Error('Could not unsubscribe this device. Retry disabling notifications.');
}

export async function showSystemNotification(title: string, options: NotificationOptions, onClick?: () => void) {
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
  options = { ...options, data: { ...options.data, createdAt: Date.now() } };
  const registration = typeof navigator !== 'undefined' && navigator.serviceWorker
    ? await navigator.serviceWorker.getRegistration() : undefined;
  if (registration) {
    await registration.showNotification(title, options);
    return;
  }
  const notification = new Notification(title, options);
  pageNotifications.add(notification);
  notification.onclose = () => { pageNotifications.delete(notification); };
  notification.onclick = () => {
    window.focus();
    if (onClick) onClick();
    else if (options.data?.url) window.location.assign(options.data.url);
    pageNotifications.delete(notification);
    notification.close();
  };
}

export async function dismissIrrelevantSessionNotifications(entries: NotifyEntry[], snapshotAt: number, signal?: AbortSignal) {
  if (!snapshotAt) return;
  const registration = typeof navigator !== 'undefined' && navigator.serviceWorker ? await navigator.serviceWorker.getRegistration() : undefined;
  const notifications = [...pageNotifications, ...(registration?.getNotifications ? await registration.getNotifications() : [])];
  if (signal?.aborted) return;
  const active = new Set<string>();
  for (const entry of entries) {
    const prefixes = [`ocman:${entry.id}`, ...(entry.platform ? [`ocman:${entry.platform}:${entry.id}`] : [])];
    for (const prefix of prefixes) {
      if (entry.pendingPermission || entry.pendingQuestion) active.add(`${prefix}:prompt`);
      if (!entry.seen && !entry.suppressTerminal && !entry.pendingPermission && !entry.pendingQuestion && entry.status !== 'busy') active.add(`${prefix}:completed`);
    }
  }
  for (const notification of notifications) {
    // A push newer than the snapshot is unknown, not irrelevant. Factory and
    // other notification categories are owned by their own state projections.
    if (notification.data?.createdAt >= snapshotAt || !/^ocman:.*:(prompt|completed)$/.test(notification.tag)) continue;
    if (!active.has(notification.tag)) { notification.close(); pageNotifications.delete(notification); }
  }
}
