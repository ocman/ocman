// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { useSessions } from '../lib/queries';
import { useUiStore } from '../lib/uiStore';
import { RootRedirect } from './RootRedirect';

vi.mock('../lib/queries', () => ({ useSessions: vi.fn() }));
function LocationMarker() { return <div data-testid="location">{useLocation().pathname}</div>; }
function renderRootRedirect() {
  render(<MemoryRouter initialEntries={['/']}><Routes><Route path="/" element={<RootRedirect />} /><Route path="/session/:id" element={<LocationMarker />} /></Routes></MemoryRouter>);
}

describe('RootRedirect', () => {
  beforeEach(() => { useUiStore.setState({ lastOpenedSessionId: undefined }); });

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
