// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import type { NotifyEntry } from '../lib/api';
import { useNotifySnapshot } from '../lib/useNotifyData';
import { useUiStore } from '../lib/uiStore';
import { MainNav } from './MainNav';

vi.mock('../lib/queries', () => ({ useInbox: () => ({ data: { items: [{ id: 'new' }], unreadTotal: 7 } }) }));
vi.mock('../lib/useNotifyData', () => ({ useNotifySnapshot: vi.fn() }));
vi.mock('../pages/SubscriptionUsage', () => ({ SubscriptionUsageContent: () => null }));
vi.mock('../features', () => ({ featureNav: [], featureNavigation: [], featureFooterNavigation: [] }));
vi.mock('./PhoneNavigation', () => ({ PhoneNavigation: ({ attention }: { attention: boolean }) => <output aria-label="Phone attention">{String(attention)}</output> }));

const unread: NotifyEntry = { id: 'one', platform: 'opencode', status: 'waiting', seen: false };
function snapshot(entries?: NotifyEntry[]) {
  vi.mocked(useNotifySnapshot).mockReturnValue(entries ? { entries, fetchedAt: 100 } : undefined);
}
function mount(onMobileClose?: () => void) {
  return render(<MemoryRouter><MainNav onMobileClose={onMobileClose} /></MemoryRouter>);
}
beforeEach(() => {
  useUiStore.setState({ mainNavCollapsed: false });
  snapshot([]);
});

it.each([undefined, []])('hides the Home badge with no unread snapshot: %j', (entries) => {
  snapshot(entries);
  mount();
  expect(screen.getByRole('link', { name: 'Home' })).toHaveTextContent(/^Home$/);
  expect(screen.getByRole('link', { name: 'Inbox, unread messages' })).toHaveTextContent(/^Inbox$/);
});

it('preserves a nonnumeric attention indicator across owners and phone navigation', () => {
  snapshot([
    unread, unread, { ...unread, platform: 'r-box:opencode' },
    { ...unread, id: 'read', seen: true },
    { ...unread, id: 'suppressed', suppressTerminal: true },
    { ...unread, id: 'question', seen: true, pendingQuestion: true },
    { ...unread, id: 'permission', seen: true, pendingPermission: true },
  ]);
  mount();
  const home = screen.getByRole('link', { name: 'Home, needs attention' });
  expect(home).toHaveTextContent(/^Home$/);
  expect(screen.getByLabelText('Phone attention')).toHaveTextContent('true');
});

it('shows no numeric badge even with many attention sessions', () => {
  useUiStore.setState({ mainNavCollapsed: true });
  snapshot(Array.from({ length: 120 }, (_, id) => ({ ...unread, id: String(id) })));
  mount();
  const home = screen.getByRole('link', { name: 'Home, needs attention' });
  expect(home).toHaveTextContent(/^Home$/);
  expect(home).toHaveAttribute('title', 'Home, needs attention');
});

it('updates and clears the badge when sessions are acknowledged, preserving Home navigation', () => {
  snapshot([unread]);
  const close = vi.fn();
  const page = mount(close);
  const home = screen.getByRole('link', { name: 'Home, needs attention' });
  expect(home).toHaveAttribute('href', '/');
  fireEvent.click(home);
  expect(close).toHaveBeenCalledOnce();
  snapshot([]);
  page.rerender(<MemoryRouter><MainNav /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Home' })).toHaveTextContent(/^Home$/);
});
