// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { WorktreesView } from './WorktreesView';
import { api } from '../lib/api';
import type { Session, WorktreeEntry } from '../lib/api';

// The view pulls in several hooks/stores that hit the network or the
// browser. Mock them to thin, deterministic stubs so the test can focus
// on refresh and delete flows.
vi.mock('../lib/headerContext', () => ({ usePageTitle: () => {} }));
// Launch affordances gate on opencodeLaunch (AD-8 / #393), not tmux.
// `launchAllowed` lets individual tests flip the flag.
const launchState = { allowed: true, askedFor: [] as (string | undefined)[] };
vi.mock('../lib/useCapabilities', () => ({
  useOpencodeLaunch: (remoteId?: string) => {
    launchState.askedFor.push(remoteId);
    return launchState.allowed;
  },
}));
const openWorktreeForm = vi.fn();
vi.mock('../lib/uiStore', () => ({
  useUiStore: (selector: (s: { openWorktreeForm: () => void }) => unknown) =>
    selector({ openWorktreeForm }),
}));
const store = { cachedSessions: [] as Session[], refreshCachedSessions: () => Promise.resolve([]) };
vi.mock('../lib/apiStore', () => ({
  useApiStore: (selector: (s: typeof store) => unknown) => selector(store),
}));

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

function SessionLocation() {
  const location = useLocation();
  return <div data-testid="session-location">{location.pathname + location.search}</div>;
}

function SwitchTo({ to }: { to: string }) {
  const navigate = useNavigate();
  return <button type="button" onClick={() => navigate(to)}>switch owner</button>;
}

function renderView(entry = '/project/%2Frepo/worktrees', switchTo?: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <div id="header-actions-slot" />
      {switchTo && <SwitchTo to={switchTo} />}
      <Routes>
        <Route path="/project/:dir/worktrees" element={<WorktreesView />} />
        <Route path="/session/:id" element={<SessionLocation />} />
      </Routes>
    </MemoryRouter>,
  );
}

function session(id: string, remoteId: string, platform: string, timeUpdated: number): Session {
  return { id, remoteId, platform, timeUpdated, directory: '/repo/.worktrees/repo/feature' } as Session;
}

describe('WorktreesView', () => {
  beforeEach(() => {
    launchState.allowed = true;
    launchState.askedFor = [];
    store.cachedSessions = [];
    openWorktreeForm.mockClear();
    vi.spyOn(api.worktree, 'list').mockResolvedValue({
      worktrees: [wt({ path: '/repo', branch: 'main', main: true }), wt()],
    });
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('blocks duplicate refreshes and re-enables the control after a failed request', async () => {
    renderView();
    await screen.findByText('feature');
    const button = screen.getByRole('button', { name: 'Refresh' });
    let rejectRefresh!: (error: Error) => void;
    vi.mocked(api.worktree.list).mockImplementationOnce(() => new Promise((_, reject) => { rejectRefresh = reject; }));
    vi.mocked(api.worktree.list).mockClear();

    fireEvent.click(button);
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('aria-busy', 'true');
    fireEvent.click(button);
    expect(api.worktree.list).toHaveBeenCalledOnce();

    rejectRefresh(new Error('Refresh failed'));
    expect(await screen.findByText('Refresh failed')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Refresh failed');
    expect(button).toBeEnabled();
    expect(button).toHaveAttribute('aria-busy', 'false');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('feature')).toBeInTheDocument();
    expect(api.worktree.list).toHaveBeenCalledTimes(2);
  });

  // AD-8 / #393: launch affordance shows when opencodeLaunch is on and
  // is fully hidden (unavailable notice) when off — independent of tmux.
  it('renders worktrees when opencodeLaunch is on', async () => {
    launchState.allowed = true;
    renderView();
    expect(await screen.findByText('feature')).toBeInTheDocument();
    expect(screen.queryByText(/unavailable on this host/i)).not.toBeInTheDocument();
  });

  it('hides the launch UI when opencodeLaunch is off', async () => {
    launchState.allowed = false;
    renderView();
    expect(await screen.findByText(/unavailable on this host/i)).toBeInTheDocument();
    expect(screen.queryByText('feature')).not.toBeInTheDocument();
  });

  it('does not offer Delete on the main worktree', async () => {
    renderView();
    await screen.findByText('feature');
    // Exactly one Delete button — for the non-main worktree.
    expect(screen.getAllByRole('button', { name: 'Delete' })).toHaveLength(1);
  });

  it('shows the shared empty state without a wide empty table', async () => {
    vi.mocked(api.worktree.list).mockResolvedValue({ worktrees: [] });
    renderView();
    expect(await screen.findByText('No worktrees found')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
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
        remoteId: 'local',
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
    expect(force).toHaveClass('oc-button--danger');
    fireEvent.click(force);

    await waitFor(() => expect(remove).toHaveBeenCalledTimes(2));
    expect(remove).toHaveBeenLastCalledWith({
      projectDir: '/repo',
      path: '/repo/.worktrees/repo/feature',
      force: true,
      remoteId: 'local',
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
      expect(screen.getByRole('alert')).toHaveTextContent('boom'),
    );
    expect(screen.queryByRole('button', { name: 'Force delete' })).not.toBeInTheDocument();
  });

  // Identical paths exist on this machine and on remote "B". The view
  // must act only on its owner: no request, count, or link may leak to
  // the other machine through directory inference.
  describe('machine ownership', () => {
    it('retries a failed read on the same explicit owner', async () => {
      vi.mocked(api.worktree.list).mockRejectedValueOnce(new Error('Owner read failed'));
      renderView('/project/%2Frepo/worktrees?remoteId=B');
      expect(await screen.findByRole('alert')).toHaveTextContent('Owner read failed');
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      expect(await screen.findByText('feature')).toBeInTheDocument();
      expect(api.worktree.list).toHaveBeenLastCalledWith('/repo', 'B');
    });
    beforeEach(() => {
      store.cachedSessions = [
        session('ses_local', 'local', 'opencode', 300),
        session('ses_b_old', 'B', 'r-B:opencode', 100),
        session('ses_b_new', 'B', 'r-B:opencode', 200),
      ];
    });

    it('defaults to this machine and links the local session', async () => {
      renderView();
      await screen.findByText('feature');
      expect(api.worktree.list).toHaveBeenCalledWith('/repo', 'local');
      expect(launchState.askedFor).toContain('local');
      expect(screen.getByTitle('ses_local')).toHaveTextContent('1');
      fireEvent.click(screen.getAllByRole('button', { name: 'Open session' })[1]);
      expect(await screen.findByTestId('session-location')).toHaveTextContent('/session/ses_local?platform=opencode');
    });

    it('scopes list, sessions, links, create, and delete to the named owner', async () => {
      const remove = vi.spyOn(api.worktree, 'remove').mockResolvedValue({ removed: true });
      renderView('/project/%2Frepo/worktrees?remoteId=B');
      await screen.findByText('feature');
      expect(api.worktree.list).toHaveBeenCalledWith('/repo', 'B');
      expect(launchState.askedFor).toContain('B');
      expect(launchState.askedFor).not.toContain(undefined);
      expect(screen.getByRole('tab', { name: 'Worktrees' })).toHaveAttribute('aria-selected', 'true');
      expect(screen.getByRole('tab', { name: 'Sessions' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'New worktree session' })).toHaveClass('oc-button--accent');
      expect(screen.queryByRole('button', { name: 'VS Code' })).not.toBeInTheDocument();

      fireEvent.click(screen.getByRole('button', { name: 'New worktree session' }));
      expect(openWorktreeForm).toHaveBeenCalledWith({ projectDir: '/repo', remoteId: 'B' });

      // Only B's two sessions belong to this worktree; the newer one opens.
      expect(screen.getByTitle('ses_b_old, ses_b_new')).toHaveTextContent('2');

      fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
      fireEvent.click(await screen.findByRole('button', { name: 'Confirm delete' }));
      await waitFor(() => expect(remove).toHaveBeenCalledWith({
        projectDir: '/repo', path: '/repo/.worktrees/repo/feature', force: false, remoteId: 'B',
      }));
      await screen.findByRole('button', { name: 'Delete' });

      fireEvent.click(screen.getAllByRole('button', { name: 'Open session' })[1]);
      expect(await screen.findByTestId('session-location')).toHaveTextContent('/session/ses_b_new?platform=r-B%3Aopencode');
    });

    // Same path on A and B: Force delete consent given on A must not
    // survive a switch to B, where it would discard B's changes.
    it('drops delete consent when the owner changes', async () => {
      const remove = vi.spyOn(api.worktree, 'remove').mockRejectedValueOnce(new Error('worktree has uncommitted changes'));
      renderView('/project/%2Frepo/worktrees?remoteId=A', '/project/%2Frepo/worktrees?remoteId=B');
      await screen.findByText('feature');
      fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
      fireEvent.click(await screen.findByRole('button', { name: 'Confirm delete' }));
      await screen.findByRole('button', { name: 'Force delete' });

      fireEvent.click(screen.getByRole('button', { name: 'switch owner' }));
      await waitFor(() => expect(api.worktree.list).toHaveBeenLastCalledWith('/repo', 'B'));
      expect(await screen.findByRole('button', { name: 'Delete' })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Force delete' })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Confirm delete' })).not.toBeInTheDocument();
      expect(remove).toHaveBeenCalledTimes(1);
      expect(remove).toHaveBeenCalledWith(expect.objectContaining({ remoteId: 'A' }));
    });

    it("ignores the previous owner's late list response", async () => {
      let resolveA!: (v: { worktrees: WorktreeEntry[] }) => void;
      vi.mocked(api.worktree.list).mockImplementation((_dir, owner) => (owner === 'A'
        ? new Promise((resolve) => { resolveA = resolve; })
        : Promise.resolve({ worktrees: [wt({ branch: 'b-branch' })] })));
      renderView('/project/%2Frepo/worktrees?remoteId=A', '/project/%2Frepo/worktrees?remoteId=B');
      await waitFor(() => expect(api.worktree.list).toHaveBeenCalledWith('/repo', 'A'));

      fireEvent.click(screen.getByRole('button', { name: 'switch owner' }));
      expect(await screen.findByText('b-branch')).toBeInTheDocument();
      resolveA({ worktrees: [wt({ branch: 'a-branch' })] });
      await new Promise((r) => setTimeout(r, 0));
      expect(screen.queryByText('a-branch')).not.toBeInTheDocument();
      expect(screen.getByText('b-branch')).toBeInTheDocument();
    });

    // Opened while B is disconnected: no doomed 503 list request, and once
    // capabilities report B connected the rows load without a Refresh.
    it('loads worktrees when a disconnected owner reconnects', async () => {
      launchState.allowed = false;
      const view = renderView('/project/%2Frepo/worktrees?remoteId=B');
      expect(await screen.findByText(/unavailable on this host/i)).toBeInTheDocument();
      expect(api.worktree.list).not.toHaveBeenCalled();

      launchState.allowed = true;
      view.rerender(
        <MemoryRouter initialEntries={['/project/%2Frepo/worktrees?remoteId=B']}>
          <div id="header-actions-slot" />
          <Routes>
            <Route path="/project/:dir/worktrees" element={<WorktreesView />} />
          </Routes>
        </MemoryRouter>,
      );
      expect(await screen.findByText('feature')).toBeInTheDocument();
      expect(api.worktree.list).toHaveBeenCalledWith('/repo', 'B');
    });

    it('shows a disconnected owner as unavailable instead of using this machine', async () => {
      launchState.allowed = false;
      renderView('/project/%2Frepo/worktrees?remoteId=gone');
      expect(await screen.findByText(/unavailable on this host/i)).toBeInTheDocument();
      expect(launchState.askedFor).toContain('gone');
      expect(launchState.askedFor).not.toContain('local');
    });
  });
});
