import { expect, it } from 'vitest';
import { withoutFactoryInbox } from './betaInbox';

it('hides Factory items and unread counts without mutating the cache', () => {
  const data = { items: [
    { id: 'general', title: 'Primary', body: '', remoteId: 'local', createdAt: 0 },
    { id: 'factory', title: 'Review', body: '', remoteId: 'local', createdAt: 0, category: 'factory' as const },
    { id: 'read', title: 'Read', body: '', remoteId: 'remote', createdAt: 0, category: 'factory' as const, readAt: 1 },
    { id: 'archived', title: 'Archived', body: '', remoteId: 'local', createdAt: 0, category: 'factory' as const, archivedAt: 1 },
  ], unreadTotal: 2 };
  expect(withoutFactoryInbox(data)).toEqual({ items: [data.items[0]], unreadTotal: 1 });
  expect(data.items).toHaveLength(4);
  expect(withoutFactoryInbox({ ...data, unreadTotal: 0 }).unreadTotal).toBe(0);
});
