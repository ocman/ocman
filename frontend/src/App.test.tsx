// @vitest-environment jsdom
import { beforeEach, describe, it, expect, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { AuthGate, MainNav, RootRedirect } from './App';
import { useAuthStore } from './lib/authStore';
import { useInbox, useSessions, useSubscriptionUsage } from './lib/queries';
import { routeTitle } from './lib/routeTitle';
import { useUiStore } from './lib/uiStore';

vi.mock('./lib/queries', () => ({
  useSessions: vi.fn(),
  useInbox: vi.fn(),
  useSubscriptionUsage: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(useInbox).mockReturnValue({ data: { items: [], unreadTotal: 0 } } as never);
  vi.mocked(useSubscriptionUsage).mockReturnValue({ data: { providers: [] } } as never);
});

describe('routeTitle', () => {
  it.each([
    ['/', 'Home'],
    ['/sessions', 'Sessions'],
    ['/inbox', 'Inbox'],
    ['/subscription-usage', 'Usage'],
    ['/analytics/overview', 'Analytics'],
    ['/analytics/performance', 'Analytics'],
    ['/settings', 'Settings'],
    ['/routines', 'Routines'],
    ['/artifacts', 'Artifacts'],
    ['/artifacts/art-1', 'Artifacts'],
    ['/factory/epics', 'Factory'],
    ['/factory/issues', 'Factory'],
    ['/factory/epics/ship-a1b2', 'Factory'],
    ['/session/new', 'New session'],
    ['/session/ses-1', 'Session'],
    ['/session/ses-1', 'Loaded title', 'Loaded title'],
    ['/project/%2Frepos%2Focman', 'repos/ocman'],
    ['/project/%2Frepos%2Focman/worktrees', 'repos/ocman / Worktrees'],
    ['/project/%2F', 'Project'],
    ['/import-share', 'Fork shared conversation'],
    ['/unknown', 'ocman'],
  ])('labels %s', (path, expected, sessionTitle?: string) => {
    expect(routeTitle(path, sessionTitle)).toBe(expected);
  });
});

describe('MainNav', () => {
  it.each([1, 12, 120])('announces %i unread messages and caps the visible badge', (unreadTotal) => {
    vi.mocked(useInbox).mockReturnValue({ data: { items: [], unreadTotal } } as never);
    useUiStore.setState({ mainNavCollapsed: true });
    render(<MemoryRouter><MainNav /></MemoryRouter>);
    const link = screen.getByRole('link', { name: `Inbox, ${unreadTotal} unread messages` });
    expect(link).toHaveTextContent(unreadTotal > 99 ? '99+' : String(unreadTotal));
    expect(link).toHaveAttribute('title', `Inbox, ${unreadTotal} unread messages`);
    useUiStore.setState({ mainNavCollapsed: false });
  });

  it('shows the app destinations and collapses from the logo', async () => {
    const user = userEvent.setup();

    render(
      <MemoryRouter initialEntries={['/projects']}>
        <MainNav />
      </MemoryRouter>,
    );

    expect(screen.getByRole('navigation', { name: 'Main navigation' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Home' })).not.toHaveClass('active');
    expect(screen.getByRole('link', { name: 'Projects' })).toHaveClass('active');
    expect(screen.getByRole('link', { name: 'Analytics' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Stats' })).not.toBeInTheDocument();
    const usage = screen.getByRole('button', { name: 'Usage' });
    const inbox = screen.getByRole('link', { name: 'Inbox' });
    // Bottom group is Inbox > Usage > Settings; Inbox carries the marker
    // class that pushes that group to the bottom of the rail.
    expect(inbox.compareDocumentPosition(usage)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(usage.compareDocumentPosition(screen.getByRole('link', { name: 'Settings' }))).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(inbox).toHaveClass('nav-bottom-start');
    expect(usage).not.toHaveClass('nav-bottom-start');
    expect(screen.getByRole('link', { name: 'Routines' })).toHaveAttribute('href', '/routines');
    expect(screen.getByRole('link', { name: 'Factory' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Sessions' }).querySelector('i')).toHaveClass('bi-collection');

    const toggle = screen.getByRole('button', { name: 'Collapse navigation' });
    await user.click(toggle);

    const expand = screen.getByRole('button', { name: 'Expand navigation' });
    expect(expand).toHaveAttribute('aria-expanded', 'false');

    await user.click(expand);
    expect(screen.getByRole('button', { name: 'Collapse navigation' })).toHaveAttribute('aria-expanded', 'true');
  });

  it('opens usage as a popover without navigating', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/projects']}>
        <MainNav />
        <LocationMarker />
      </MemoryRouter>,
    );

    const usage = screen.getByRole('button', { name: 'Usage' });
    await user.click(usage);

    expect(screen.getByRole('dialog', { name: 'Subscription usage' })).toBeInTheDocument();
    expect(screen.getByTestId('location')).toHaveTextContent('/projects');
    expect(usage).toHaveAttribute('aria-expanded', 'true');

    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog', { name: 'Subscription usage' })).not.toBeInTheDocument();
  });

  it('marks Home active on a session detail route', () => {
    render(
      <MemoryRouter initialEntries={['/session/sess-1']}>
        <MainNav />
      </MemoryRouter>,
    );

    expect(screen.getByRole('link', { name: 'Home' })).toHaveClass('active');
  });

  // One row per route in App.tsx: every page highlights exactly its owning destination.
  it.each([
    ['/session/sess-1', 'Home'],
    ['/sessions', 'Sessions'],
    ['/projects', 'Projects'],
    ['/project/%2Frepo', 'Projects'],
    ['/project/%2Frepo/worktrees', 'Projects'],
    ['/project/%2Frepo/settings', 'Projects'],
    ['/factory/overview', 'Factory'],
    ['/factory/how-to', 'Factory'],
    ['/factory/epics', 'Factory'],
    ['/factory/epics/epic-1', 'Factory'],
    ['/factory/issues', 'Factory'],
    ['/factory/issues/issue-1', 'Factory'],
    ['/factory/queue', 'Factory'],
    ['/factory/configuration', 'Factory'],
    ['/routines', 'Routines'],
    ['/artifacts', 'Artifacts'],
    ['/artifacts/a-1', 'Artifacts'],
    ['/analytics/overview', 'Analytics'],
    ['/analytics/logs', 'Analytics'],
    ['/inbox', 'Inbox'],
    ['/subscription-usage', 'Usage'],
    ['/settings', 'Settings'],
  ])('marks only the owning destination active on %s', (path, label) => {
    render(<MemoryRouter initialEntries={[path]}><MainNav /></MemoryRouter>);
    const nav = screen.getByRole('navigation', { name: 'Main navigation' });
    const active = [...nav.querySelectorAll('.active')].map((el) => el.querySelector('span')?.textContent);
    expect(active).toEqual([label]);
  });
});

function LocationMarker() {
  return <div data-testid="location">{useLocation().pathname}</div>;
}

function renderRootRedirect() {
  render(
    <MemoryRouter initialEntries={['/']}>
      <Routes>
        <Route path="/" element={<RootRedirect />} />
        <Route path="/session/:id" element={<LocationMarker />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('RootRedirect', () => {
  beforeEach(() => {
    useUiStore.setState({ lastOpenedSessionId: undefined });
  });

  it('prefers the most recently opened active session', async () => {
    useUiStore.setState({ lastOpenedSessionId: 'opened' });
    vi.mocked(useSessions).mockReturnValue({ isLoading: false, data: [
      { id: 'active-latest', archived: false, timeUpdated: 200 },
      { id: 'opened', archived: false, timeUpdated: 100 },
    ] } as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/opened');
  });

  it('falls back to latest activity when the last opened session is archived', async () => {
    useUiStore.setState({ lastOpenedSessionId: 'opened' });
    vi.mocked(useSessions).mockReturnValue({ isLoading: false, data: [
      { id: 'older', archived: false, timeUpdated: 100 },
      { id: 'opened', archived: true, timeUpdated: 300 },
      { id: 'active-latest', archived: false, timeUpdated: 200 },
    ] } as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/active-latest');
  });

  it('redirects to a new session when every session is archived', async () => {
    useUiStore.setState({ lastOpenedSessionId: 'archived' });
    vi.mocked(useSessions).mockReturnValue({ isLoading: false, data: [{ id: 'archived', archived: true, timeUpdated: 300 }] } as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/new');
  });

  it('redirects to the latest session', async () => {
    vi.mocked(useSessions).mockReturnValue({ isLoading: false, data: [{ id: 'latest' }] } as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/latest');
  });

  it('redirects to new session when none exist', async () => {
    vi.mocked(useSessions).mockReturnValue({ isLoading: false, data: [] } as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/new');
  });

  it('shows an error with a retry instead of redirecting when the query fails', () => {
    const refetch = vi.fn();
    vi.mocked(useSessions).mockReturnValue({ isLoading: false, isError: true, error: new Error('backend is not responding'), data: undefined, refetch } as never);
    renderRootRedirect();
    expect(screen.queryByTestId('location')).not.toBeInTheDocument();
    expect(screen.getByText(/backend is not responding/)).toBeInTheDocument();
    screen.getByRole('button', { name: /retry/i }).click();
    expect(refetch).toHaveBeenCalled();
  });

  it('treats a settled query with no payload as no sessions', async () => {
    vi.mocked(useSessions).mockReturnValue({ isLoading: false, isError: false, data: undefined } as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/new');
  });

  it('renders nothing while the query is still loading', () => {
    vi.mocked(useSessions).mockReturnValue({ isLoading: true, data: undefined } as never);
    renderRootRedirect();
    expect(screen.queryByTestId('location')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument();
  });
});

describe('AuthGate', () => {
  it('replaces the spinner with the backend banner after the boot timeout', () => {
    vi.useFakeTimers();
    try {
      const bootstrap = vi.fn(() => new Promise<void>(() => {}));
      useAuthStore.setState({ checking: true, bootstrap });
      render(<AuthGate><p>app</p></AuthGate>);
      expect(screen.getByText('Checking authentication…')).toBeInTheDocument();
      act(() => { vi.advanceTimersByTime(8_000); });
      expect(screen.getByTestId('backend-status-banner')).toHaveTextContent('Backend is not responding.');
      act(() => { screen.getByRole('button', { name: 'Retry' }).click(); });
      expect(bootstrap).toHaveBeenCalledTimes(2);
      act(() => { useAuthStore.setState({ checking: false }); });
      expect(screen.getByText('app')).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});
