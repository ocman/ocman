// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { WorktreeFormModal } from './WorktreeFormModal';

// A worktree form opened for a remote-owned project must create, look up
// the base ref, gate, seed, and link on that owner — never on whichever
// machine happens to share the path.
let uiState: Record<string, unknown> = {};
vi.mock('../lib/uiStore', () => ({
  useUiStore: (sel: (s: Record<string, unknown>) => unknown) => sel(uiState),
}));
vi.mock('../lib/api', () => ({
  api: {
    gitBranches: vi.fn().mockResolvedValue({ branches: ['main', 'develop'] }),
    worktree: {
      defaultBaseRef: vi.fn().mockResolvedValue({ baseRef: 'main' }),
      createAndLaunch: vi.fn(),
    },
  },
}));
const askedFor: (string | undefined)[] = [];
vi.mock('../lib/useCapabilities', () => ({
  useOpencodeLaunch: (remoteId?: string) => {
    askedFor.push(remoteId);
    return remoteId !== 'gone';
  },
}));
const seedNewSession = vi.fn();
const projectsLoader = vi.hoisted(() => vi.fn());
vi.mock('../lib/apiStore', () => ({
  useApiStore: (sel: (s: Record<string, unknown>) => unknown) =>
    sel({ getProjects: projectsLoader, seedNewSession }),
}));

import { api } from '../lib/api';
const wt = api.worktree as unknown as {
  defaultBaseRef: ReturnType<typeof vi.fn>;
  createAndLaunch: ReturnType<typeof vi.fn>;
};

beforeEach(() => {
  wt.defaultBaseRef.mockResolvedValue({ baseRef: 'main' });
  projectsLoader.mockResolvedValue([
    { directory: '/repo', remoteId: 'B' }, { directory: '/other', remoteId: 'B' },
    { directory: '/local' }, { directory: '/foreign', remoteId: 'A' },
  ]);
});

afterEach(() => {
  vi.clearAllMocks();
  askedFor.length = 0;
});

function Where() {
  const l = useLocation();
  return <div data-testid="where">{l.pathname + l.search}</div>;
}

function openFor(remoteId: string | undefined) {
  uiState = {
    worktreeFormOpen: true,
    worktreeFormGen: 1,
    worktreeFormProject: '/repo',
    worktreeFormRemoteId: remoteId,
    closeWorktreeForm: vi.fn(),
  };
  return render(
    <MemoryRouter initialEntries={['/']}>
      <WorktreeFormModal />
      <Routes>
        <Route path="/" element={null} />
        <Route path="/session/:id" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('WorktreeFormModal machine ownership', () => {
  it('uses the shared modal, header, fields and action controls', async () => {
    openFor('B');
    const dialog = screen.getByRole('dialog', { name: 'New worktree session' });
    expect(dialog).toHaveClass('oc-modal');
    expect(within(dialog).getByTestId('modal-header')).toBeInTheDocument();
    for (const button of within(dialog).getAllByRole('button')) expect(button).toHaveClass('oc-button');
    for (const input of within(dialog).getAllByRole('textbox')) expect(input).toHaveClass('oc-field');
    expect(screen.getByRole('combobox', { name: 'Project' })).toHaveTextContent('/repo');
    expect(screen.getByRole('button', { name: 'Create & launch' })).toHaveClass('oc-button--accent');
    await waitFor(() => expect(screen.getByLabelText('Base ref')).toHaveValue('main'));
  });

  it('blocks all dismissal actions and edits while creation is pending', async () => {
    let reject!: (error: unknown) => void;
    wt.createAndLaunch.mockImplementation(() => new Promise((_, fail) => { reject = fail; }));
    openFor('B');
    await waitFor(() => expect(screen.getByLabelText('Base ref')).toHaveValue('main'));
    fireEvent.change(screen.getByLabelText('Branch'), { target: { value: 'feature/settings' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create & launch' }));
    expect(screen.getByLabelText('Branch')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Close worktree session dialog' })).toBeDisabled();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(uiState.closeWorktreeForm).not.toHaveBeenCalled();
    await act(async () => { reject(new Error('Creation failed')); });
    expect(screen.getByRole('alert')).toHaveTextContent('Creation failed');
    expect(screen.getByLabelText('Branch')).toBeEnabled();
  });

  it('keeps base-ref validation when creating a new branch', async () => {
    wt.defaultBaseRef.mockReturnValue(new Promise(() => {}));
    openFor('B');
    fireEvent.change(screen.getByLabelText('Branch'), { target: { value: 'feature/settings' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create & launch' }));
    expect(screen.getByRole('alert')).toHaveTextContent('Base ref is required');
    expect(wt.createAndLaunch).not.toHaveBeenCalled();
  });

  it('searches owner-local projects and preserves an explicit ref on reselect', async () => {
    const user = userEvent.setup();
    wt.defaultBaseRef.mockImplementation(async (dir: string) => ({ baseRef: dir === '/other' ? 'trunk' : 'main' }));
    openFor('B');
    await waitFor(() => expect(screen.getByLabelText('Base ref')).toHaveValue('main'));
    await user.click(screen.getByRole('combobox', { name: 'Project' }));
    expect(screen.queryByRole('option', { name: '/local' })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: '/foreign' })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText('Search projects'), 'other');
    await user.click(screen.getByRole('option', { name: '/other' }));
    await waitFor(() => expect(screen.getByLabelText('Base ref')).toHaveValue('trunk'));
    fireEvent.change(screen.getByLabelText('Base ref'), { target: { value: 'refs/heads/develop' } });
    await user.click(screen.getByRole('combobox', { name: 'Project' }));
    await user.click(screen.getByRole('option', { name: '/other' }));
    expect(screen.getByLabelText('Base ref')).toHaveValue('refs/heads/develop');
    expect(wt.defaultBaseRef).toHaveBeenCalledTimes(2);
  });

  it('ignores a previous project default arriving after a project switch', async () => {
    const user = userEvent.setup();
    let resolve!: (value: { baseRef: string }) => void;
    wt.defaultBaseRef.mockImplementation((dir: string) => dir === '/repo'
      ? new Promise((done) => { resolve = done; }) : Promise.resolve({ baseRef: 'trunk' }));
    openFor('B');
    await user.click(screen.getByRole('combobox', { name: 'Project' }));
    await user.click(await screen.findByRole('option', { name: '/other' }));
    await waitFor(() => expect(screen.getByLabelText('Base ref')).toHaveValue('trunk'));
    await act(async () => { resolve({ baseRef: 'old-main' }); });
    expect(screen.getByLabelText('Base ref')).toHaveValue('trunk');
  });

  it('does not overwrite a custom ref entered while the default is loading', async () => {
    let resolve!: (value: { baseRef: string }) => void;
    wt.defaultBaseRef.mockImplementation(() => new Promise((done) => { resolve = done; }));
    openFor('B');
    fireEvent.change(screen.getByLabelText('Base ref'), { target: { value: 'refs/tags/release' } });
    await act(async () => { resolve({ baseRef: 'refs/heads/main' }); });
    expect(screen.getByLabelText('Base ref')).toHaveValue('refs/tags/release');
  });

  it.each(['refs/tags/release', 'refs/remotes/origin/develop', 'abc1234', 'refs/heads/release', 'HEAD~2'])('submits the custom base %s without an ambiguous branch catalog', async (baseRef) => {
    wt.createAndLaunch.mockResolvedValue({ worktreePath: '/.worktrees/repo/x' });
    openFor('B');
    await waitFor(() => expect(screen.getByLabelText('Base ref')).toHaveValue('main'));
    fireEvent.change(screen.getByLabelText('Branch'), { target: { value: 'feature/custom' } });
    fireEvent.change(screen.getByLabelText('Base ref'), { target: { value: baseRef } });
    fireEvent.click(screen.getByRole('button', { name: 'Create & launch' }));
    await waitFor(() => expect(wt.createAndLaunch).toHaveBeenCalledWith(expect.objectContaining({ baseRef, remoteId: 'B' })));
    expect(api.gitBranches).not.toHaveBeenCalled();
  });

  it('lets the user retry loading the default branch without recreating the dialog', async () => {
    wt.defaultBaseRef.mockRejectedValueOnce(new Error('default branch unavailable'));
    openFor('B');
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load base refs');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.getByLabelText('Base ref')).toHaveValue('main'));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(wt.defaultBaseRef).toHaveBeenCalledTimes(2);
  });

  it('creates on the named remote and opens the session there', async () => {
    wt.createAndLaunch.mockResolvedValue({
      sessionId: 'ses_b', worktreePath: '/.worktrees/repo/x', branch: 'x',
      reused: false, branchExisted: false, platform: 'r-B:opencode', remoteId: 'B',
    });
    openFor('B');
    await waitFor(() => expect(wt.defaultBaseRef).toHaveBeenCalledWith('/repo', 'B', expect.anything()));
    expect(askedFor).toContain('B');
    fireEvent.change(screen.getByPlaceholderText('feature/login'), { target: { value: 'x' } });
    await waitFor(() => expect(screen.getByLabelText('Base ref')).toHaveValue('main'));
    fireEvent.click(screen.getByRole('button', { name: 'Create & launch' }));

    expect(await screen.findByTestId('where')).toHaveTextContent('/session/ses_b?platform=r-B%3Aopencode');
    expect(wt.createAndLaunch).toHaveBeenCalledWith(expect.objectContaining({ projectDir: '/repo', remoteId: 'B' }));
    expect(seedNewSession).toHaveBeenCalledWith('ses_b', '/.worktrees/repo/x', 'r-B:opencode', 'x', 'B');
  });

  it('keeps a local project on the local owner even when no remote id was supplied', async () => {
    wt.createAndLaunch.mockResolvedValue({ sessionId: 'ses_local', worktreePath: '/.worktrees/repo/x', platform: 'opencode' });
    openFor(undefined);
    await waitFor(() => expect(screen.getByLabelText('Base ref')).toHaveValue('main'));
    fireEvent.change(screen.getByLabelText('Branch'), { target: { value: 'feature/local' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create & launch' }));
    await waitFor(() => expect(wt.createAndLaunch).toHaveBeenCalledWith(expect.objectContaining({ remoteId: 'local' })));
    expect(seedNewSession).toHaveBeenCalledWith('ses_local', '/.worktrees/repo/x', 'opencode', 'feature/local', 'local');
  });

  it('refuses to open for a disconnected owner instead of using this machine', async () => {
    openFor('gone');
    expect(await screen.findByText('Worktree sessions unavailable')).toBeInTheDocument();
    expect(askedFor).toEqual(['gone']);
    expect(wt.defaultBaseRef).not.toHaveBeenCalled();
  });
});
