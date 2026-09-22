// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { vi, describe, it, expect, beforeEach } from 'vitest';
import { api } from '../lib/api';
import { useShortcutDispatcher } from '../lib/shortcutRegistry';
import { Inbox } from './Inbox';

/** Routes keydown events to registered shortcuts, as App does. */
function Shortcuts() {
  useShortcutDispatcher();
  return null;
}

const items = [{ id: '1', title: 'Build **finished**', body: 'See [details](https://example.com).', createdAt: Date.now(), remoteId: 'local', category: 'general' as const }, { id: '2', title: 'Remote note', body: 'body', createdAt: Date.now() - 1_000, readAt: Date.now(), remoteId: 'laptop', category: 'factory' as const }];

function renderInbox() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return { client, ...render(<QueryClientProvider client={client}><MemoryRouter><Shortcuts /><Inbox /></MemoryRouter></QueryClientProvider>) };
}

function inboxAction(name: string) {
  const toggle = screen.getByLabelText('Inbox actions');
  if (!toggle.closest('details')?.open) fireEvent.click(toggle);
  return screen.getByRole('button', { name });
}

function statusFilter(name: string) {
  return within(screen.getByRole('group', { name: 'Message status' })).getByRole('button', { name });
}

describe('Inbox', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(api, 'inbox').mockResolvedValue({ items, unreadTotal: 1 });
    vi.spyOn(api, 'markInboxItemRead').mockResolvedValue(undefined);
    vi.spyOn(api, 'markInboxItemUnread').mockResolvedValue(undefined);
    vi.spyOn(api, 'respondPermission').mockResolvedValue(undefined);
    vi.spyOn(api, 'archiveInboxItems').mockResolvedValue(undefined);
    vi.spyOn(api, 'archiveAllReadInboxItems').mockResolvedValue(undefined);
  });

  it('opens markdown in the reading pane, marks unread items read, and archives selection', async () => {
    renderInbox();
    const title = await screen.findByRole('button', { name: /Build.*finished/ });
    // The row shows no category name — the icon carries the category.
    expect(within(title).queryByText('Primary')).toBeNull();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    fireEvent.click(title);
    expect(screen.getByRole('link', { name: 'details' })).toHaveAttribute('href', 'https://example.com');
    await waitFor(() => expect(api.markInboxItemRead).toHaveBeenCalledWith('1', 'local'));
    fireEvent.click(screen.getByRole('checkbox', { name: /Build/ }));
    expect(within(screen.getByRole('region', { name: 'Message body' })).getByRole('link', { name: 'details' })).toBeInTheDocument();
    expect(title).toHaveAttribute('aria-current', 'true');
    fireEvent.click(inboxAction('Archive selected (1)'));
    await waitFor(() => expect(api.archiveInboxItems).toHaveBeenCalledWith([{ id: '1', remoteId: 'local' }]));
  });

  it('opens the newest message on arrival and archives it with Delete or Backspace', async () => {
    renderInbox();
    const reader = screen.getByRole('region', { name: 'Message body' });
    expect(within(await within(reader).findByTestId('inbox-message-header')).getByRole('heading', { name: 'Build finished' })).toBeInTheDocument();
    await waitFor(() => expect(api.markInboxItemRead).toHaveBeenCalledWith('1', 'local'));

    fireEvent.keyDown(document.body, { code: 'Backspace', key: 'Backspace' });
    await waitFor(() => expect(api.archiveInboxItems).toHaveBeenCalledWith([{ id: '1', remoteId: 'local' }]));
    // The reader is cleared, so a second Delete can't re-archive it.
    vi.mocked(api.archiveInboxItems).mockClear();
    fireEvent.keyDown(document.body, { code: 'Delete', key: 'Delete' });
    expect(api.archiveInboxItems).not.toHaveBeenCalled();
  });

  it('leaves the archived view alone when Delete is pressed', async () => {
    renderInbox();
    await screen.findByText('Remote note');
    fireEvent.click(statusFilter('Archived'));
    fireEvent.keyDown(document.body, { code: 'Delete', key: 'Delete' });
    expect(api.archiveInboxItems).not.toHaveBeenCalled();
  });

  it('archives all read items for each source', async () => {
    renderInbox();
    await screen.findByText('Remote note');
    fireEvent.click(inboxAction('Archive all read'));
    await waitFor(() => expect(api.archiveAllReadInboxItems).toHaveBeenCalledWith('local'));
    expect(api.archiveAllReadInboxItems).toHaveBeenCalledWith('laptop');
  });

  it('selects one message type at a time and labels only the active type', async () => {
    renderInbox();
    await screen.findByText('Remote note');
    const factoryFilter = screen.getByRole('button', { name: 'Factory' });
    expect(factoryFilter).toHaveAttribute('title', 'Factory');
    expect(factoryFilter.textContent).toBe('');
    expect(factoryFilter.querySelector('i')).toHaveClass('bi-buildings');
    fireEvent.click(statusFilter('Unread'));
    expect(screen.queryByText('Remote note')).not.toBeInTheDocument();
    expect(statusFilter('Unread')).toHaveAttribute('aria-pressed', 'true');
    expect(statusFilter('Unread')).toHaveTextContent('Unread');
    expect(statusFilter('All').textContent).toBe('');
    fireEvent.click(factoryFilter);
    const typeGroup = screen.getByRole('group', { name: 'Message type' });
    expect(within(typeGroup).getAllByRole('button').map((button) => button.getAttribute('title'))).toEqual(['All', 'Primary', 'Factory', 'Routines', 'Permissions']);
    expect(within(typeGroup).getAllByRole('button', { pressed: true })).toEqual([factoryFilter]);
    expect(factoryFilter).toHaveTextContent('Factory');
    expect(within(typeGroup).getByRole('button', { name: 'All' }).textContent).toBe('');
    expect(screen.getByText('No messages match these filters.')).toBeInTheDocument();
    fireEvent.click(statusFilter('All'));
    expect(screen.getByText('Remote note')).toBeInTheDocument();
    expect(screen.queryByText('Build **finished**')).not.toBeInTheDocument();
    fireEvent.click(within(typeGroup).getByRole('button', { name: 'All' }));
    expect(factoryFilter.textContent).toBe('');
    fireEvent.click(statusFilter('All'));
    expect(screen.getByText('Build **finished**')).toBeInTheDocument();
  });

  it('switches messages, returns to the list, and clears the reader when filtering', async () => {
    renderInbox();
    fireEvent.click(await screen.findByRole('button', { name: /Build.*finished/ }));
    fireEvent.click(screen.getByRole('button', { name: /Remote note/ }));
    const reader = screen.getByRole('region', { name: 'Message body' });
    expect(within(reader).getByRole('heading', { name: 'Remote note' })).toBeInTheDocument();
    expect(within(reader).queryByRole('link', { name: 'details' })).not.toBeInTheDocument();
    expect(api.markInboxItemRead).not.toHaveBeenCalledWith('2', 'laptop');
    fireEvent.click(screen.getByRole('button', { name: 'Back to messages' }));
    expect(within(reader).getByText('Select a message')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /Remote note/ }));
    fireEvent.click(statusFilter('Unread'));
    expect(within(reader).getByText('Select a message')).toBeInTheDocument();
  });

  it('archives the open message and clears the reader when it disappears', async () => {
    renderInbox();
    fireEvent.click(await screen.findByRole('button', { name: /Remote note/ }));
    vi.mocked(api.inbox).mockResolvedValue({ items: [items[0]], unreadTotal: 1 });
    fireEvent.click(screen.getByRole('button', { name: 'Archive message' }));
    await waitFor(() => expect(api.archiveInboxItems).toHaveBeenCalledWith([{ id: '2', remoteId: 'laptop' }]));
    await screen.findByText('Select a message');
  });

  it('keeps matching IDs from different sources independent and allows deselection', async () => {
    vi.mocked(api.inbox).mockResolvedValue({ items: [items[0], { ...items[1], id: '1' }], unreadTotal: 1 });
    renderInbox();
    const first = await screen.findByRole('checkbox', { name: /Build/ });
    fireEvent.click(first);
    fireEvent.click(first);
    expect(inboxAction('Archive selected')).toBeDisabled();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Remote note' }));
    fireEvent.click(inboxAction('Archive selected (1)'));
    await waitFor(() => expect(api.archiveInboxItems).toHaveBeenCalledWith([{ id: '1', remoteId: 'laptop' }]));
  });

  it('shows loading and empty states', async () => {
    vi.mocked(api.inbox).mockResolvedValue({ items: [], unreadTotal: 0 });
    renderInbox();
    expect(screen.getByRole('status')).toHaveTextContent('Loading inbox');
    await screen.findByText('Your inbox is empty.');
    expect(inboxAction('Archive all read')).toBeDisabled();
  });

  it('treats older messages without a category as general', async () => {
    vi.mocked(api.inbox).mockResolvedValue({ items: [{ ...items[0], category: undefined }], unreadTotal: 1 });
    renderInbox();
    const message = await screen.findByRole('button', { name: /Build.*finished/ });
    expect(screen.getByRole('checkbox', { name: /Build/ }).querySelector('i')).toHaveClass('bi-chat-left-text');
    fireEvent.click(screen.getByRole('button', { name: 'Primary' }));
    expect(message).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Factory' }));
    expect(screen.getByText('No messages match these filters.')).toBeInTheDocument();
  });

  it('reports load errors without showing an empty inbox', async () => {
    vi.mocked(api.inbox).mockRejectedValue(new Error('offline'));
    renderInbox();
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load inbox.');
    expect(screen.queryByText('Your inbox is empty.')).not.toBeInTheDocument();
  });

  it('reports read and archive failures while keeping the message open', async () => {
    vi.mocked(api.markInboxItemRead).mockRejectedValue(new Error('offline'));
    vi.mocked(api.archiveInboxItems).mockRejectedValue(new Error('offline'));
    renderInbox();
    fireEvent.click(await screen.findByRole('button', { name: /Build.*finished/ }));
    await screen.findByText('Could not mark the message as read. Open it again to retry.');
    fireEvent.click(screen.getByRole('button', { name: 'Archive message' }));
    await screen.findByText('Could not archive messages. Please try again.');
    expect(screen.getByRole('link', { name: 'details' })).toBeInTheDocument();
  });

  it('marks the open message unread, updates the count, and returns to the list', async () => {
    const { client } = renderInbox();
    fireEvent.click(await screen.findByRole('button', { name: /Remote note/ }));
    vi.mocked(api.inbox).mockResolvedValue({ items: items.map((item) => ({ ...item, readAt: undefined })), unreadTotal: 2 });
    fireEvent.click(screen.getByRole('button', { name: 'Mark unread' }));
    await waitFor(() => expect(api.markInboxItemUnread).toHaveBeenCalledWith('2', 'laptop'));
    await screen.findByText('Select a message');
    expect(client.getQueryData(['inbox'])).toMatchObject({ unreadTotal: 2 });
    fireEvent.click(screen.getByRole('button', { name: /Remote note/ }));
    await waitFor(() => expect(api.markInboxItemRead).toHaveBeenCalledWith('2', 'laptop'));
  });

  it('keeps the reader and read state when marking unread fails', async () => {
    vi.mocked(api.markInboxItemUnread).mockRejectedValue(new Error('offline'));
    const { client } = renderInbox();
    fireEvent.click(await screen.findByRole('button', { name: /Remote note/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Mark unread' }));
    await screen.findByText('Could not mark the message as unread. Please try again.');
    expect(screen.getByRole('heading', { name: 'Remote note' })).toBeInTheDocument();
    expect(client.getQueryData(['inbox'])).toMatchObject({ unreadTotal: 1 });
  });

  it('shows permission actions and routes replies to the owning platform', async () => {
    const permission = { platform: 'r-laptop:opencode', sessionId: 'child-session', permissionId: 'perm-1', permission: 'bash', patterns: ['git status'], metadata: { command: 'git status' } };
    vi.mocked(api.inbox).mockResolvedValue({ items: [{ ...items[1], category: 'permission', permission }], unreadTotal: 0 });
    renderInbox();
    fireEvent.click(await screen.findByRole('button', { name: /Remote note/ }));
    expect(screen.getByRole('button', { name: 'Allow once' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Allow always' })).toBeInTheDocument();
    expect(within(screen.getByTestId('inbox-message-header')).getByRole('link', { name: 'child-session' })).toHaveAttribute('href', '/session/child-session?platform=r-laptop%3Aopencode');
    vi.mocked(api.respondPermission).mockRejectedValueOnce(new Error('offline'));
    fireEvent.click(screen.getByRole('button', { name: 'Reject' }));
    await screen.findByText('Could not send your response. Please try again.');
    expect(api.respondPermission).toHaveBeenCalledWith('child-session', 'perm-1', 'reject', 'r-laptop:opencode');
    expect(screen.getByRole('heading', { name: 'Remote note' })).toBeInTheDocument();
    vi.mocked(api.inbox).mockResolvedValue({ items: [], unreadTotal: 0 });
    fireEvent.click(screen.getByRole('button', { name: 'Reject' }));
    await screen.findByText('Select a message');
    expect(screen.queryByRole('button', { name: 'Reject' })).not.toBeInTheDocument();
  });

  it('fuzzy searches message titles and bodies and clears the selected message', async () => {
    renderInbox();
    fireEvent.click(await screen.findByRole('button', { name: /Remote note/ }));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search inbox' }), { target: { value: 'bldfsh' } });
    await waitFor(() => expect(screen.queryByText('Remote note')).not.toBeInTheDocument());
    expect(screen.getByText('Build **finished**')).toBeInTheDocument();
    expect(screen.getByText('Select a message')).toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search inbox' }), { target: { value: 'bdy' } });
    await waitFor(() => expect(screen.queryByText('Build **finished**')).not.toBeInTheDocument());
    expect(screen.getByText('Remote note')).toBeInTheDocument();
  });

  it.each([
    ['general', 'chat-left-text'], ['factory', 'buildings'], ['routine', 'clock-history'], ['permission', 'shield-lock'],
  ] as const)('shows the %s icon as the selection control and a session link in the header', async (category, icon) => {
    vi.mocked(api.inbox).mockResolvedValue({ items: [{ ...items[0], category, session: { platform: 'r-laptop:opencode', sessionId: 'ses/source', title: 'Fix deployment' } }], unreadTotal: 1 });
    renderInbox();
    const message = await screen.findByRole('button', { name: /Build.*finished/ });
    const select = screen.getByRole('checkbox', { name: /Build/ });
    expect(select.querySelector('i')).toHaveClass(`bi-${icon}`);
    expect(select.querySelector('i')).toHaveAttribute('aria-hidden', 'true');
    // Selecting swaps the category icon for a check mark.
    fireEvent.click(select);
    expect(select).toHaveAttribute('aria-checked', 'true');
    expect(select.querySelector('i')).toHaveClass('bi-check-square-fill');
    fireEvent.click(select);
    fireEvent.click(message);
    expect(within(screen.getByTestId('inbox-message-header')).getByRole('link', { name: 'Fix deployment' })).toHaveAttribute('href', '/session/ses%2Fsource?platform=r-laptop%3Aopencode');
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search inbox' }), { target: { value: 'Fix deployment' } });
    expect(await screen.findByRole('button', { name: /Build.*finished/ })).toBeInTheDocument();
  });

  it('shows an explicit missing-session label for legacy messages', async () => {
    renderInbox();
    fireEvent.click(await screen.findByRole('button', { name: /Build.*finished/ }));
    expect(within(screen.getByTestId('inbox-message-header')).getByText('Originating session unavailable')).toBeInTheDocument();
  });

  it('browses archived messages without changing unread counts or offering resolved permission actions', async () => {
    const permission = { platform: 'opencode', sessionId: 'session', permissionId: 'request', permission: 'bash', patterns: [] };
    vi.mocked(api.inbox).mockImplementation(async (_signal, archived) => archived
      ? { items: [{ ...items[0], title: 'Archived permission', archivedAt: Date.now(), permission }], unreadTotal: 0 }
      : { items, unreadTotal: 1 });
    const { client } = renderInbox();
    await screen.findByText('Remote note');
    fireEvent.click(statusFilter('Archived'));
    // Ignore the read from auto-selecting the first message on load.
    vi.mocked(api.markInboxItemRead).mockClear();
    fireEvent.click(await screen.findByRole('button', { name: /Archived permission/ }));
    expect(api.markInboxItemRead).not.toHaveBeenCalled();
    expect(client.getQueryData(['inbox'])).toMatchObject({ unreadTotal: 1 });
    expect(screen.queryByRole('region', { name: 'Permission actions' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Mark unread' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Archive message' })).toBeDisabled();
    expect(inboxAction('Archive all read')).toBeDisabled();
    fireEvent.keyDown(screen.getByLabelText('Inbox actions'), { key: 'Escape' });
    expect(screen.getByLabelText('Inbox actions').closest('details')).not.toHaveAttribute('open');
    fireEvent.click(statusFilter('All'));
    expect(await screen.findByText('Remote note')).toBeInTheDocument();
    expect(screen.queryByText('Archived permission')).not.toBeInTheDocument();
  });
});
