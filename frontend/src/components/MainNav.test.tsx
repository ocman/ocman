// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import type { NotifyEntry, Session } from '../lib/api';
import { useSidebarSessionList } from '../lib/sessionsCache';
import { useNotifySnapshot } from '../lib/useNotifyData';
import { useUiStore } from '../lib/uiStore';
import { MainNav } from './MainNav';
import { useInbox } from '../lib/queries';

vi.mock('../lib/queries', () => ({ useInbox: vi.fn() }));
vi.mock('../lib/useNotifyData', () => ({ useNotifySnapshot: vi.fn() }));
vi.mock('../lib/sessionsCache', () => ({ useSidebarSessionList: vi.fn() }));
vi.mock('../pages/SubscriptionUsage', () => ({ SubscriptionUsageContent: () => null }));
vi.mock('../features', () => ({ featureNav: [], featureNavigation: [], featureFooterNavigation: [] }));
vi.mock('./PhoneNavigation', () => ({ PhoneNavigation: ({ unreadSessions, unreadTotal }: { unreadSessions: number; unreadTotal: number }) => <><output aria-label="Phone unread sessions">{unreadSessions}</output><output aria-label="Phone unread inbox">{unreadTotal}</output></> }));

const unread: NotifyEntry = { id: 'one', platform: 'opencode', status: 'waiting', seen: false };
it('does not add session creation to main navigation', () => {
  mount();
  expect(screen.queryByRole('button', { name: 'New one-off session' })).not.toBeInTheDocument();
});
function snapshot(entries?: NotifyEntry[]) {
  vi.mocked(useNotifySnapshot).mockReturnValue(entries ? { entries, fetchedAt: 100 } : undefined);
}
function mount(onMobileClose?: () => void) {
  return render(<MemoryRouter><MainNav onMobileClose={onMobileClose} /></MemoryRouter>);
}
beforeEach(() => {
  vi.mocked(useInbox).mockReturnValue({ data: { items: [{ id: 'new' }], unreadTotal: 7 } } as never);
  useUiStore.setState({ mainNavCollapsed: false, sidebarView: 'projects' });
  vi.mocked(useSidebarSessionList).mockReturnValue([]);
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

it('does not count notification-only history hidden from the recent sidebar', () => {
  useUiStore.setState({ sidebarView: 'recent' });
  snapshot([{ ...unread, id: 'old-factory', status: 'interrupted' }]);
  vi.mocked(useSidebarSessionList).mockReturnValue([
    { ...unread, id: 'running', status: 'busy' },
    { ...unread, id: 'viewed', seen: true },
  ] as Session[]);
  mount();
  expect(screen.getByRole('link', { name: 'Home' })).toHaveTextContent(/^Home$/);
  expect(screen.getByLabelText('Phone unread sessions')).toHaveTextContent('0');
});

it('counts only actionable recent sidebar rows using their current state', () => {
  useUiStore.setState({ sidebarView: 'recent' });
  snapshot([unread, ...['stale', 'viewed', 'factory', 'child', 'routine', 'plugin', 'archive', 'sleeping']
    .map(id => ({ ...unread, id, status: 'interrupted' })),
  { ...unread, id: 'prompt', seen: true, pendingQuestion: true }]);
  vi.mocked(useSidebarSessionList).mockReturnValue([
    unread,
    { ...unread, id: 'stale', status: 'busy' },
    { ...unread, id: 'viewed', seen: true },
    { ...unread, id: 'factory', factoryAttemptId: 'attempt' },
    { ...unread, id: 'child', parentId: 'factory' },
    { ...unread, id: 'routine', routineId: 'run' },
    { ...unread, id: 'plugin', origin: 'plugin' },
    { ...unread, id: 'archive', archived: true },
    { ...unread, id: 'sleeping', snoozedUntil: Date.now() + 60_000 },
    { ...unread, id: 'prompt', seen: true, pendingQuestion: true },
  ] as Session[]);
  mount();
  expect(screen.getByRole('link', { name: 'Home, 2 unread sessions' })).toHaveTextContent('2');
  expect(screen.getByLabelText('Phone unread sessions')).toHaveTextContent('2');
});

it('clears recent badges on a sidebar update without waiting for the notify poll', () => {
  useUiStore.setState({ sidebarView: 'recent' });
  snapshot([unread]);
  vi.mocked(useSidebarSessionList).mockReturnValue([unread] as Session[]);
  const page = mount();
  expect(screen.getByRole('link', { name: 'Home, 1 unread sessions' })).toBeInTheDocument();
  vi.mocked(useSidebarSessionList).mockReturnValue([{ ...unread, status: 'busy' }] as Session[]);
  page.rerender(<MemoryRouter><MainNav /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Home' })).toHaveTextContent(/^Home$/);
  expect(screen.getByLabelText('Phone unread sessions')).toHaveTextContent('0');
});
