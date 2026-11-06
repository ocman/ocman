// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import type { NotifyEntry } from '../lib/api';
import { useNotifySnapshot } from '../lib/useNotifyData';
import { useUiStore } from '../lib/uiStore';
import { MainNav } from './MainNav';
import { useInbox } from '../lib/queries';

vi.mock('../lib/queries', () => ({ useInbox: vi.fn() }));
vi.mock('../lib/useNotifyData', () => ({ useNotifySnapshot: vi.fn() }));
vi.mock('../pages/SubscriptionUsage', () => ({ SubscriptionUsageContent: () => null }));
vi.mock('../features', () => ({ featureNav: [], featureNavigation: [], featureFooterNavigation: [] }));
vi.mock('./PhoneNavigation', () => ({ PhoneNavigation: ({ unreadSessions, unreadTotal }: { unreadSessions: number; unreadTotal: number }) => <><output aria-label="Phone unread sessions">{unreadSessions}</output><output aria-label="Phone unread inbox">{unreadTotal}</output></> }));

const unread: NotifyEntry = { id: 'one', platform: 'opencode', status: 'waiting', seen: false };
function snapshot(entries?: NotifyEntry[]) {
  vi.mocked(useNotifySnapshot).mockReturnValue(entries ? { entries, fetchedAt: 100 } : undefined);
}
function mount(onMobileClose?: () => void) {
  return render(<MemoryRouter><MainNav onMobileClose={onMobileClose} /></MemoryRouter>);
}
beforeEach(() => {
  vi.mocked(useInbox).mockReturnValue({ data: { items: [{ id: 'new' }], unreadTotal: 7 } } as never);
  useUiStore.setState({ mainNavCollapsed: false });
  snapshot([]);
});

it.each([undefined, [], [{ id: 'read', readAt: 1 }, { id: 'archived', archivedAt: 1 }]])('hides the Inbox badge without unread active items: %j', (items) => {
  vi.mocked(useInbox).mockReturnValue({ data: items ? { items, unreadTotal: 7 } : undefined } as never);
  mount();
  expect(screen.getByRole('link', { name: 'Inbox' })).toHaveTextContent(/^Inbox$/);
});

it('counts only unread unarchived Inbox items and caps the collapsed badge', () => {
  useUiStore.setState({ mainNavCollapsed: true });
  vi.mocked(useInbox).mockReturnValue({ data: { items: [
    ...Array.from({ length: 120 }, (_, id) => ({ id: String(id) })),
    { id: 'read', readAt: 1 }, { id: 'archived', archivedAt: 1 },
  ], unreadTotal: 0 } } as never);
  mount();
  const inbox = screen.getByRole('link', { name: 'Inbox, 120 unread messages' });
  expect(inbox).toHaveTextContent('99+');
  expect(inbox).toHaveAttribute('title', 'Inbox, 120 unread messages');
  expect(within(inbox).getByText('99+')).toHaveAttribute('aria-hidden', 'true');
});

it('updates and clears the Inbox count when items are read or archived', () => {
  const page = mount();
  expect(screen.getByRole('link', { name: 'Inbox, 1 unread messages' })).toHaveTextContent('1');
  vi.mocked(useInbox).mockReturnValue({ data: { items: [{ id: 'new', readAt: 1 }], unreadTotal: 0 } } as never);
  page.rerender(<MemoryRouter><MainNav /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Inbox' })).toHaveTextContent(/^Inbox$/);
  vi.mocked(useInbox).mockReturnValue({ data: { items: [{ id: 'new' }], unreadTotal: 0 } } as never);
  page.rerender(<MemoryRouter><MainNav /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Inbox, 1 unread messages' })).toHaveTextContent('1');
  vi.mocked(useInbox).mockReturnValue({ data: { items: [{ id: 'new', archivedAt: 1 }], unreadTotal: 0 } } as never);
  page.rerender(<MemoryRouter><MainNav /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Inbox' })).toHaveTextContent(/^Inbox$/);
});

it.each([undefined, []])('hides the Home badge with no unread snapshot: %j', (entries) => {
  snapshot(entries);
  mount();
  expect(screen.getByRole('link', { name: 'Home' })).toHaveTextContent(/^Home$/);
  expect(screen.getByRole('link', { name: 'Inbox, 1 unread messages' })).toHaveTextContent('1');
  expect(screen.getByLabelText('Phone unread inbox')).toHaveTextContent('1');
});

it('counts distinct attention sessions across owners and phone navigation', () => {
  snapshot([
    unread, unread, { ...unread, platform: 'r-box:opencode' },
    { ...unread, id: 'read', seen: true },
    { ...unread, id: 'suppressed', suppressTerminal: true },
    { ...unread, id: 'question', seen: true, pendingQuestion: true },
    { ...unread, id: 'permission', seen: true, pendingPermission: true },
  ]);
  mount();
  const home = screen.getByRole('link', { name: 'Home, 4 unread sessions' });
  expect(home).toHaveTextContent('4');
  expect(within(home).getByText('4')).toHaveAttribute('aria-hidden', 'true');
  expect(screen.getByLabelText('Phone unread sessions')).toHaveTextContent('4');
});

it('caps the visible badge but announces the full count when collapsed', () => {
  useUiStore.setState({ mainNavCollapsed: true });
  snapshot(Array.from({ length: 120 }, (_, id) => ({ ...unread, id: String(id) })));
  mount();
  const home = screen.getByRole('link', { name: 'Home, 120 unread sessions' });
  expect(home).toHaveTextContent('99+');
  expect(home).toHaveAttribute('title', 'Home, 120 unread sessions');
});

it('updates and clears the badge when sessions are acknowledged, preserving Home navigation', () => {
  snapshot([unread]);
  const close = vi.fn();
  const page = mount(close);
  const home = screen.getByRole('link', { name: 'Home, 1 unread sessions' });
  expect(home).toHaveAttribute('href', '/');
  fireEvent.click(home);
  expect(close).toHaveBeenCalledOnce();
  snapshot([]);
  page.rerender(<MemoryRouter><MainNav /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Home' })).toHaveTextContent(/^Home$/);
});
