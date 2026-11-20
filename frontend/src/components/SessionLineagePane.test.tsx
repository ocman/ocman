// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { api, type Session } from '../lib/api';
import { SessionLineagePane } from './SessionLineagePane';

const current = { id: 'current', platform: 'test', title: 'Current task', status: 'busy', timeCreated: 1 } as Session;

function show(session?: Session, sessionTree?: Session[]) {
  const client = new QueryClient();
  return render(<QueryClientProvider client={client}><MemoryRouter>
    <SessionLineagePane session={session} sessionTree={sessionTree} />
  </MemoryRouter></QueryClientProvider>);
}

describe('SessionLineagePane', () => {
  it('does not load the catalog for an unstarted session', () => {
    const load = vi.spyOn(api, 'sessions');
    show();
    expect(screen.getByText(/available after the session starts/)).toBeVisible();
    expect(load).not.toHaveBeenCalled();
  });

  it('keeps native links visible through a failed catalog lookup and retries', async () => {
    const child = { ...current, id: 'child', title: 'Native child', parentId: current.id, archived: true };
    vi.spyOn(api, 'sessions').mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce([]);
    show({ ...current, parentId: 'missing' }, [child]);
    expect(screen.getByText(/looking up related sessions/i)).toBeVisible();
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load');
    expect(screen.getByRole('link', { name: /Native child/ })).toBeVisible();
    expect(screen.getByText('Archived')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Parent session unavailable.')).toBeVisible();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows an empty state only after a successful lookup', async () => {
    vi.spyOn(api, 'sessions').mockResolvedValueOnce([]);
    show(current);
    expect(await screen.findByText('No linked sessions found.')).toBeVisible();
    expect(screen.getByRole('link', { name: /Current task/ })).toHaveAttribute('aria-current', 'page');
  });
});
