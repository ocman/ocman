import type { InboxResponse } from './api.inbox.types';

/** Keep query-cache data intact while hiding disabled automation from readers. */
export function withoutFactoryInbox(data: InboxResponse): InboxResponse {
  const items = data.items.filter((item) => item.category !== 'factory');
  const hiddenUnread = data.items.filter((item) => item.category === 'factory' && !item.readAt && !item.archivedAt).length;
  return { ...data, items, unreadTotal: Math.max(0, data.unreadTotal - hiddenUnread) };
}
