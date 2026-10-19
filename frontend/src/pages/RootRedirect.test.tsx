// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { Link, MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { useSessions } from '../lib/queries';
import { useUiStore } from '../lib/uiStore';
import { RootRedirect } from './RootRedirect';
import { useIsPhone } from '../lib/useIsPhone';
import { useSidebarSessionList } from '../lib/sessionsCache';

vi.mock('../lib/queries', () => ({ useSessions: vi.fn() }));
vi.mock('../lib/useIsPhone', () => ({ useIsPhone: vi.fn(() => false) }));
vi.mock('../lib/sessionsCache', () => ({ useSidebarSessionList: vi.fn(() => []) }));
function LocationMarker() { return <div data-testid="location">{useLocation().pathname}</div>; }
function renderRootRedirect() {
  render(<MemoryRouter initialEntries={['/']}><Routes><Route path="/" element={<RootRedirect />} /><Route path="/session/:id" element={<LocationMarker />} /><Route path="/sessions" element={<LocationMarker />} /></Routes></MemoryRouter>);
}

describe('RootRedirect', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(useIsPhone).mockReturnValue(false);
    vi.mocked(useSidebarSessionList).mockReturnValue([]);
    useUiStore.setState({ lastOpenedSessionId: undefined });
  });

  it.each([
    { isLoading: false, data: [{ id: 'opened', timeUpdated: 100 }] },
    { isLoading: false, data: [] },
    { isLoading: true },
    { isLoading: false, isError: true, error: new Error('Offline') },
  ])('opens the mobile session list without waiting for session restoration: %j', async (query) => {
    vi.mocked(useIsPhone).mockReturnValue(true);
    vi.mocked(useSessions).mockReturnValue(query as never);
    useUiStore.setState({ lastOpenedSessionId: 'opened' });
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/sessions');
    expect(useSessions).not.toHaveBeenCalled();
    expect(useUiStore.getState().lastOpenedSessionId).toBe('opened');
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

  it('opens the cached last session without waiting for a full session-list fetch', async () => {
    useUiStore.setState({ lastOpenedSessionId: 'opened' });
    vi.mocked(useSidebarSessionList).mockReturnValue([
      { id: 'newer', archived: false, timeUpdated: 200 },
      { id: 'opened', archived: false, timeUpdated: 100 },
    ] as never);
    vi.mocked(useSessions).mockReturnValue({ isLoading: true, data: undefined } as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/opened');
    expect(useSessions).not.toHaveBeenCalled();
  });

  it('returns from another page through Home without mounting the full-list query', async () => {
    useUiStore.setState({ lastOpenedSessionId: 'opened' });
    vi.mocked(useSidebarSessionList).mockReturnValue([{ id: 'opened', timeUpdated: 100 }] as never);
    vi.mocked(useSessions).mockReturnValue({ isLoading: true, data: undefined } as never);
    render(<MemoryRouter initialEntries={['/settings']}><Routes>
      <Route path="/settings" element={<Link to="/">Home</Link>} />
      <Route path="/" element={<RootRedirect />} />
      <Route path="/session/:id" element={<LocationMarker />} />
    </Routes></MemoryRouter>);
    fireEvent.click(screen.getByRole('link', { name: 'Home' }));
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/opened');
    expect(useSessions).not.toHaveBeenCalled();
  });

  it('uses the newest active cached session when no last-opened session is saved', async () => {
    vi.mocked(useSidebarSessionList).mockReturnValue([
      { id: 'old', timeUpdated: 100 },
      { id: 'archived', archived: true, timeUpdated: 300 },
      { id: 'latest', timeUpdated: 200 },
    ] as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/latest');
    expect(useSessions).not.toHaveBeenCalled();
  });

  it('skips a cached archived last-opened session without fetching a full list', async () => {
    useUiStore.setState({ lastOpenedSessionId: 'archived' });
    vi.mocked(useSidebarSessionList).mockReturnValue([
      { id: 'archived', archived: true, timeUpdated: 300 },
      { id: 'active', timeUpdated: 100 },
    ] as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/active');
    expect(useSessions).not.toHaveBeenCalled();
  });

  it('still finds a saved session outside the sidebar cache', async () => {
    useUiStore.setState({ lastOpenedSessionId: 'older-opened' });
    vi.mocked(useSidebarSessionList).mockReturnValue([{ id: 'cached-newer', timeUpdated: 300 }] as never);
    vi.mocked(useSessions).mockReturnValue({ isLoading: false, data: [
      { id: 'cached-newer', timeUpdated: 300 },
      { id: 'older-opened', timeUpdated: 100 },
    ] } as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/older-opened');
    expect(useSessions).toHaveBeenCalled();
  });

  it('queries the full list rather than treating an archived-only cache as authoritative', async () => {
    vi.mocked(useSidebarSessionList).mockReturnValue([{ id: 'archived', archived: true, timeUpdated: 300 }] as never);
    vi.mocked(useSessions).mockReturnValue({ isLoading: false, data: [{ id: 'outside-cache', timeUpdated: 100 }] } as never);
    renderRootRedirect();
    expect(await screen.findByTestId('location')).toHaveTextContent('/session/outside-cache');
    expect(useSessions).toHaveBeenCalled();
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
    expect(screen.getByRole('alert')).toHaveTextContent('backend is not responding');
    expect(screen.getByRole('button', { name: /retry/i })).toHaveClass('oc-button');
    screen.getByRole('button', { name: /retry/i }).click();
    expect(refetch).toHaveBeenCalled();
  });

  it('uses the fallback error and disables retry while refetching', () => {
    vi.mocked(useSessions).mockReturnValue({ isLoading: false, isError: true, isFetching: true, error: 'unexpected', refetch: vi.fn() } as never);
    renderRootRedirect();
    expect(screen.getByRole('alert')).toHaveTextContent('Could not load sessions.');
    expect(screen.getByRole('button', { name: /retry/i })).toBeDisabled();
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
