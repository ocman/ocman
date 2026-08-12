// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { RootRedirect } from './App';
import { useSessions } from './lib/queries';

vi.mock('./lib/queries', () => ({
  useSessions: vi.fn(),
}));

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

  it('renders nothing while the query is still loading', () => {
    vi.mocked(useSessions).mockReturnValue({ isLoading: true, data: undefined } as never);

    renderRootRedirect();

    expect(screen.queryByTestId('location')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument();
  });
});
