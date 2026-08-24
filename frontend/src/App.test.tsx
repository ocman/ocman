// @vitest-environment jsdom
import { beforeEach, describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { MainNav, RootRedirect } from './App';
import { useSessions } from './lib/queries';
import { routeTitle } from './lib/routeTitle';
import { useUiStore } from './lib/uiStore';

vi.mock('./lib/queries', () => ({
  useSessions: vi.fn(),
}));

describe('routeTitle', () => {
  it.each([
    ['/sessions', 'Sessions'],
    ['/settings', 'Settings'],
    ['/session/new', 'New session'],
    ['/session/ses-1', 'Session'],
    ['/session/ses-1', 'Loaded title', 'Loaded title'],
    ['/project/%2Frepos%2Focman', 'ocman'],
    ['/project/%2Frepos%2Focman/worktrees', 'ocman / Worktrees'],
    ['/project/%2F', 'Project'],
    ['/import-share', 'Fork shared conversation'],
    ['/unknown', 'ocman'],
  ])('labels %s', (path, expected, sessionTitle?: string) => {
    expect(routeTitle(path, sessionTitle)).toBe(expected);
  });
});

describe('MainNav', () => {
  it('shows the app destinations and collapses from the logo', async () => {
    const user = userEvent.setup();

    render(
      <MemoryRouter initialEntries={['/projects']}>
        <MainNav workflowsAllowed />
      </MemoryRouter>,
    );

    expect(screen.getByRole('navigation', { name: 'Main navigation' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Home' })).not.toHaveClass('active');
    expect(screen.getByRole('link', { name: 'Projects' })).toHaveClass('active');
    expect(screen.getByRole('link', { name: 'Workflows' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Sessions' }).querySelector('i')).toHaveClass('bi-collection');

    const toggle = screen.getByRole('button', { name: 'Collapse navigation' });
    await user.click(toggle);

    const expand = screen.getByRole('button', { name: 'Expand navigation' });
    expect(expand).toHaveAttribute('aria-expanded', 'false');

    await user.click(expand);
    expect(screen.getByRole('button', { name: 'Collapse navigation' })).toHaveAttribute('aria-expanded', 'true');
  });

  it('marks Home active on a session detail route', () => {
    render(
      <MemoryRouter initialEntries={['/session/sess-1']}>
        <MainNav />
      </MemoryRouter>,
    );

    expect(screen.getByRole('link', { name: 'Home' })).toHaveClass('active');
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
    vi.mocked(useSessions).mockReturnValue({
      isLoading: false,
      data: [
        { id: 'active-latest', archived: false, timeUpdated: 200 },
        { id: 'opened', archived: false, timeUpdated: 100 },
      ],
    } as never);

    renderRootRedirect();

    expect(await screen.findByTestId('location')).toHaveTextContent('/session/opened');
  });

  it('falls back to latest activity when the last opened session is archived', async () => {
    useUiStore.setState({ lastOpenedSessionId: 'opened' });
    vi.mocked(useSessions).mockReturnValue({
      isLoading: false,
      data: [
        { id: 'older', archived: false, timeUpdated: 100 },
        { id: 'opened', archived: true, timeUpdated: 300 },
        { id: 'active-latest', archived: false, timeUpdated: 200 },
      ],
    } as never);

    renderRootRedirect();

    expect(await screen.findByTestId('location')).toHaveTextContent('/session/active-latest');
  });

  it('redirects to a new session when every session is archived', async () => {
    useUiStore.setState({ lastOpenedSessionId: 'archived' });
    vi.mocked(useSessions).mockReturnValue({
      isLoading: false,
      data: [{ id: 'archived', archived: true, timeUpdated: 300 }],
    } as never);

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

  // A failed query is not "no sessions". Redirecting to /session/new on
  // failure hides a backend outage behind the new-session onboarding
  // screen, and strands the user away from the session they had open.
  it('shows an error with a retry instead of redirecting when the query fails', async () => {
    const refetch = vi.fn();
    vi.mocked(useSessions).mockReturnValue({
      isLoading: false,
      isError: true,
      error: new Error('backend is not responding'),
      data: undefined,
      refetch,
    } as never);

    renderRootRedirect();

    expect(screen.queryByTestId('location')).not.toBeInTheDocument();
    expect(screen.getByText(/backend is not responding/)).toBeInTheDocument();

    screen.getByRole('button', { name: /retry/i }).click();
    expect(refetch).toHaveBeenCalled();
  });

  // Only `isError` means the query failed. A settled query with an
  // undefined payload is still a success, and labelling it a failure
  // would show a backend-outage banner over a working backend.
  it('treats a settled query with no payload as no sessions', async () => {
    vi.mocked(useSessions).mockReturnValue({
      isLoading: false,
      isError: false,
      data: undefined,
    } as never);

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
