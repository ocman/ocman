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
});
