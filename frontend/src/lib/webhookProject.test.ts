import { expect, it, vi } from 'vitest';
import { api } from './api';
import type { WebhookInbox } from './api.types';
import { triggerFor, saveTrigger } from './webhookFilters';

vi.mock('./api', () => ({ api: { webhookInboxes: { subscribe: vi.fn(), unsubscribe: vi.fn() } } }));

it('round-trips the project pointer without changing webhook filters', async () => {
  const inboxes = [{ id: 'i', subscriptions: [{ routineId: 'r', headerPredicates: '{}', jsonPredicates: '{}', projectPointer: '/repository/clone_url' }] }] as WebhookInbox[];
  const trigger = triggerFor('r', inboxes);
  expect(trigger.projectPointer).toBe('/repository/clone_url');
  await saveTrigger('r', trigger, inboxes);
  expect(api.webhookInboxes.subscribe).toHaveBeenCalledWith('i', { routineId: 'r', headerPredicates: '{}', jsonPredicates: '{}', projectPointer: '/repository/clone_url' });
});
