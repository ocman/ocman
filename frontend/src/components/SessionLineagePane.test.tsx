// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api, type Session } from '../lib/api';
import { prWorkflowApi, type PRLink } from '../lib/prWorkflowApi';
import { SessionLineagePane } from './SessionLineagePane';

const current = { id: 'current', platform: 'test', title: 'Current task', status: 'busy', timeCreated: 1 } as Session;

function show(session?: Session, sessionTree?: Session[]) {
  const client = new QueryClient();
  return render(<QueryClientProvider client={client}><MemoryRouter>
    <SessionLineagePane session={session} sessionTree={sessionTree} />
  </MemoryRouter></QueryClientProvider>);
}

describe('SessionLineagePane', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(prWorkflowApi, 'background').mockResolvedValue({ pullRequests: [] });
  });

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

  it('labels other projects and links saved pull requests', async () => {
    const worktree = { ...current, id: 'wt', title: 'Worktree child', parentId: current.id, directory: '/src/.worktrees/app/fix' };
    const other = { ...current, id: 'other', title: 'Infra child', parentId: current.id, directory: '/src/infra' };
    vi.spyOn(api, 'sessions').mockResolvedValueOnce([worktree, other]);
    const prs = vi.spyOn(prWorkflowApi, 'background').mockImplementation(async id => ({ pullRequests: id === 'other' ? [{
      number: 7, remote: { host: 'forge.example', repo: 'acme/infra' }, pr: { title: 'Rotate keys', status: 'merged', url: 'https://forge.example/acme/infra/pulls/7' },
    } as PRLink] : [] }));
    show({ ...current, directory: '/src/app' });
    const pr = await screen.findByRole('link', { name: '#7' });
    expect(pr).toHaveAttribute('href', 'https://forge.example/acme/infra/pulls/7');
    expect(pr).toHaveAttribute('data-status', 'merged');
    expect(screen.getByTitle('/src/infra')).toBeVisible();
    expect(screen.queryByTitle('/src/app')).not.toBeInTheDocument();
    expect(prs).toHaveBeenCalledWith('other', 'test', expect.any(AbortSignal));
  });

  it('reports a failed PR lookup and retries it', async () => {
    vi.spyOn(api, 'sessions').mockResolvedValueOnce([]);
    vi.spyOn(prWorkflowApi, 'background').mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ pullRequests: [{
      number: 9, remote: { host: 'forge.example', repo: 'acme/app' }, pr: { title: 'Fix', status: 'open', url: 'https://forge.example/acme/app/pulls/9' },
    } as PRLink] });
    show(current);
    fireEvent.click(await screen.findByRole('button', { name: 'Could not load PRs for Current task. Retry' }));
    expect(await screen.findByRole('link', { name: '#9' })).toBeVisible();
    expect(screen.queryByRole('button', { name: /Could not load PRs/ })).not.toBeInTheDocument();
  });

  it('shows only the current session when it has no relatives', async () => {
    vi.spyOn(api, 'sessions').mockResolvedValueOnce([]);
    show(current);
    await waitFor(() => expect(screen.queryByText(/looking up related sessions/i)).not.toBeInTheDocument());
    expect(screen.getAllByRole('link')).toHaveLength(1);
    expect(screen.getByRole('link', { name: /Current task/ })).toHaveAttribute('aria-current', 'page');
    expect(screen.queryByText(/no linked sessions/i)).not.toBeInTheDocument();
  });
});
