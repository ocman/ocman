// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { vi, describe, it, expect, beforeEach } from 'vitest';
import { api } from '../lib/api';
import { Inbox } from './Inbox';

const items = [{ id: '1', title: 'Build **finished**', body: 'See [details](https://example.com).', createdAt: Date.now(), remoteId: 'local' }, { id: '2', title: 'Remote note', body: 'body', createdAt: Date.now() - 1_000, readAt: Date.now(), remoteId: 'laptop' }];

function renderInbox() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><MemoryRouter><Inbox /></MemoryRouter></QueryClientProvider>);
}

describe('Inbox', () => {
  beforeEach(() => {
    vi.spyOn(api, 'inbox').mockResolvedValue({ items, unreadTotal: 1 });
    vi.spyOn(api, 'markInboxItemRead').mockResolvedValue(undefined);
    vi.spyOn(api, 'archiveInboxItems').mockResolvedValue(undefined);
    vi.spyOn(api, 'archiveAllReadInboxItems').mockResolvedValue(undefined);
  });

  it('expands markdown, shows sources, marks unread items read, and archives selection', async () => {
    renderInbox();
    const title = await screen.findByRole('button', { name: /Build.*finished/ });
    expect(screen.getByText('This machine')).toBeInTheDocument();
    expect(screen.getByText('laptop')).toBeInTheDocument();
    fireEvent.click(title);
    expect(screen.getByRole('link', { name: 'details' })).toHaveAttribute('href', 'https://example.com');
    await waitFor(() => expect(api.markInboxItemRead).toHaveBeenCalledWith('1', 'local'));
    fireEvent.click(screen.getByRole('checkbox', { name: /Build/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Archive selected' }));
    await waitFor(() => expect(api.archiveInboxItems).toHaveBeenCalledWith([{ id: '1', remoteId: 'local' }]));
  });

  it('archives all read items for each source', async () => {
    renderInbox();
    await screen.findByText('Remote note');
    fireEvent.click(screen.getByRole('button', { name: 'Archive all read' }));
    await waitFor(() => expect(api.archiveAllReadInboxItems).toHaveBeenCalledWith('local'));
    expect(api.archiveAllReadInboxItems).toHaveBeenCalledWith('laptop');
  });
});
