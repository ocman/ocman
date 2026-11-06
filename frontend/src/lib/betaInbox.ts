import type { InboxResponse } from './api.inbox.types';

/** Keep query-cache data intact while hiding disabled automation from readers. */
export function withoutFactoryInbox(data: InboxResponse): InboxResponse {
  const items = data.items.filter((item) => item.category !== 'factory');
  return { ...data, items, unreadTotal: 0 };
}
