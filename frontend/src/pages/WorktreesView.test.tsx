// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { WorktreesView } from './WorktreesView';
import { api } from '../lib/api';
import type { WorktreeEntry } from '../lib/api';

// The view pulls in several hooks/stores that hit the network or the
// browser. Mock them to thin, deterministic stubs so the test can focus
// on the delete flow (the only logic this suite cares about).
vi.mock('../lib/headerContext', () => ({ usePageTitle: () => {} }));
vi.mock('../lib/useCapabilities', () => ({ useWorktreeSessions: () => true }));
vi.mock('../lib/shortcuts', () => ({ openVSCode: () => {} }));
vi.mock('../lib/uiStore', () => ({
  useUiStore: (selector: (s: { openWorktreeForm: () => void }) => unknown) =>
    selector({ openWorktreeForm: vi.fn() }),
}));
vi.mock('../lib/apiStore', () => ({
  useApiStore: (
    selector: (s: { cachedSessions: never[]; refreshCachedSessions: () => Promise<never[]> }) => unknown,
  ) => selector({ cachedSessions: [], refreshCachedSessions: () => Promise.resolve([]) }),
}));

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useParams: () => ({ dir: encodeURIComponent('/repo') }) };
});

function wt(overrides: Partial<WorktreeEntry> = {}): WorktreeEntry {
  return {
    path: '/repo/.worktrees/repo/feature',
    branch: 'feature',
    head: 'abc',
    bare: false,
    locked: false,
    main: false,
    ...overrides,
  };
}

function renderView() {
  return render(
    <MemoryRouter>
      <WorktreesView />
    </MemoryRouter>,
  );
}

describe('WorktreesView delete flow', () => {
  beforeEach(() => {
    vi.spyOn(api.worktree, 'list').mockResolvedValue({
      worktrees: [wt({ path: '/repo', branch: 'main', main: true }), wt()],
    });
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('does not offer Delete on the main worktree', async () => {
    renderView();
    await screen.findByText('feature');
    // Exactly one Delete button — for the non-main worktree.
    expect(screen.getAllByRole('button', { name: 'Delete' })).toHaveLength(1);
  });

  it('Delete arms a confirm, then removes and reloads on success', async () => {
    const remove = vi.spyOn(api.worktree, 'remove').mockResolvedValue({ removed: true });
    renderView();
    await screen.findByText('feature');

    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    // Two-step: confirm must appear, nothing removed yet.
    const confirm = await screen.findByRole('button', { name: 'Confirm delete' });
    expect(remove).not.toHaveBeenCalled();

    fireEvent.click(confirm);
    await waitFor(() =>
      expect(remove).toHaveBeenCalledWith({
        projectDir: '/repo',
        path: '/repo/.worktrees/repo/feature',
        force: false,
      }),
    );
  });

  it('offers Force delete after a dirty 409 and forces on retry', async () => {
    const remove = vi
      .spyOn(api.worktree, 'remove')
      .mockRejectedValueOnce(new Error('worktree has uncommitted changes'))
      .mockResolvedValueOnce({ removed: true });
    renderView();
    await screen.findByText('feature');

    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Confirm delete' }));

    const force = await screen.findByRole('button', { name: 'Force delete' });
    fireEvent.click(force);

    await waitFor(() => expect(remove).toHaveBeenCalledTimes(2));
    expect(remove).toHaveBeenLastCalledWith({
      projectDir: '/repo',
      path: '/repo/.worktrees/repo/feature',
      force: true,
    });
  });

  it('surfaces a non-dirty error instead of arming Force delete', async () => {
    vi.spyOn(api.worktree, 'remove').mockRejectedValue(new Error('boom'));
    renderView();
    await screen.findByText('feature');

    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Confirm delete' }));

    // Non-dirty error is surfaced; the dirty-only Force delete is not armed.
    await waitFor(() =>
      expect(document.querySelector('.oc-list-error')?.textContent).toBe('boom'),
    );
    expect(screen.queryByRole('button', { name: 'Force delete' })).not.toBeInTheDocument();
  });
});
