// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation } from 'react-router-dom';
import * as Toast from '@radix-ui/react-toast';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { FactoryActionNotify } from './FactoryActionNotify';
import { PromptToastViewport } from './PromptToast';
import type { InboxItem } from '../lib/api';
import { useUiStore } from '../lib/uiStore';

vi.mock('../lib/api', () => ({ api: { inbox: vi.fn(() => new Promise(() => {})) } }));
const item = (id: string, extra: Partial<InboxItem> = {}): InboxItem => ({ id: `factory-action-${id}`, remoteId: 'local', category: 'factory', title: 'Ship it', body: 'Review', createdAt: 1, ...extra });
const notifications = vi.fn();
beforeEach(() => {
  useUiStore.setState({ notificationsEnabled: true });
  vi.stubGlobal('Notification', Object.assign(function(...args: unknown[]) { notifications(...args); }, { permission: 'granted' }));
});
afterEach(() => { vi.unstubAllGlobals(); notifications.mockClear(); });
function Location() { const location = useLocation(); return <output aria-label="Route">{location.pathname}{location.search}</output>; }
function setup(initial?: InboxItem[]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (initial) client.setQueryData(['inbox'], { items: initial, unreadTotal: initial.length });
  const view = render(<QueryClientProvider client={client}><MemoryRouter><Toast.Provider><FactoryActionNotify /><PromptToastViewport /></Toast.Provider><Location /></MemoryRouter></QueryClientProvider>);
  const update = (items: InboxItem[]) => act(() => { client.setQueryData(['inbox'], { items, unreadTotal: items.length }); });
  return { update, ...view };
}
it('notifies only new actions, deduplicates refreshes and opens Factory', async () => {
  const { update } = setup([item('old')]);
  expect(screen.queryByText('Factory action required')).not.toBeInTheDocument();
  update([item('old'), item('new')]);
  expect(await screen.findByText('Factory action required')).toBeVisible();
  expect(notifications).toHaveBeenCalledTimes(1);
  expect(notifications.mock.calls[0][1].data.url).toBe('/factory/overview');
  update([item('old'), item('new')]);
  expect(notifications).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('button', { name: 'Open actions' }));
  expect(screen.getByLabelText('Route')).toHaveTextContent('/factory/overview');
  expect(screen.queryByText('Factory action required')).not.toBeInTheDocument();
});
it('uses the first fetched snapshot as baseline and ignores read and ordinary items', async () => {
  const { update } = setup();
  update([item('old')]);
  update([item('old'), item('read', { readAt: 2 }), item('ordinary', { id: 'other' })]);
  expect(notifications).not.toHaveBeenCalled();
  update([item('new')]);
  expect(await screen.findByText('Factory action required')).toBeVisible();
  update([]);
  await waitFor(() => expect(screen.queryByText('Factory action required')).not.toBeInTheDocument());
});
it('keeps in-app notifications when OS notifications are disabled and dismisses them', async () => {
  useUiStore.setState({ notificationsEnabled: false });
  const { update } = setup([]);
  update([item('new')]);
  expect(await screen.findByText('Factory action required')).toBeVisible();
  expect(notifications).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  expect(screen.queryByText('Factory action required')).not.toBeInTheDocument();
});
it('routes remote actions to their Inbox and tolerates unavailable OS notifications', async () => {
  vi.stubGlobal('Notification', undefined);
  const { update } = setup([]);
  update([item('remote', { remoteId: 'owner' })]);
  fireEvent.click(await screen.findByRole('button', { name: 'Open actions' }));
  expect(screen.getByLabelText('Route')).toHaveTextContent('/inbox?category=factory');
});
it('keeps the toast when notification permission is denied or construction fails', async () => {
  const { update } = setup([]);
  vi.stubGlobal('Notification', Object.assign(function() { throw new Error('unsupported'); }, { permission: 'denied' }));
  update([item('denied')]);
  expect(await screen.findByText('Factory action required')).toBeVisible();
  vi.stubGlobal('Notification', Object.assign(function() { throw new Error('unsupported'); }, { permission: 'granted' }));
  update([item('throw')]);
  await waitFor(() => expect(screen.getAllByText('Factory action required')).toHaveLength(1));
});
it('focuses and navigates when a system notification is clicked', () => {
  const focus = vi.spyOn(window, 'focus').mockImplementation(() => {});
  const close = vi.fn();
  let notification: { onclick?: () => void; close: () => void };
  vi.stubGlobal('Notification', Object.assign(function() { notification = { close }; return notification; }, { permission: 'granted' }));
  const { update } = setup([]);
  update([item('click')]);
  act(() => notification!.onclick!());
  expect(focus).toHaveBeenCalled();
  expect(close).toHaveBeenCalled();
  expect(screen.getByLabelText('Route')).toHaveTextContent('/factory/overview');
  focus.mockRestore();
});
