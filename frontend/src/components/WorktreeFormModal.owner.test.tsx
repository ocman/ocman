// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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
vi.mock('../lib/apiStore', () => ({
  useApiStore: (sel: (s: Record<string, unknown>) => unknown) =>
    sel({ getProjects: vi.fn().mockResolvedValue([]), seedNewSession }),
}));

import { api } from '../lib/api';
const wt = api.worktree as unknown as {
  defaultBaseRef: ReturnType<typeof vi.fn>;
  createAndLaunch: ReturnType<typeof vi.fn>;
};

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
  it('creates on the named remote and opens the session there', async () => {
    wt.createAndLaunch.mockResolvedValue({
      sessionId: 'ses_b', worktreePath: '/.worktrees/repo/x', branch: 'x',
      reused: false, branchExisted: false, platform: 'r-B:opencode', remoteId: 'B',
    });
    openFor('B');
    await waitFor(() => expect(wt.defaultBaseRef).toHaveBeenCalledWith('/repo', 'B', expect.anything()));
    expect(askedFor).toContain('B');
    fireEvent.change(screen.getByPlaceholderText('feature/login'), { target: { value: 'x' } });
    await waitFor(() => expect(screen.getByPlaceholderText('main')).toHaveValue('main'));
    fireEvent.click(screen.getByRole('button', { name: 'Create & launch' }));

    expect(await screen.findByTestId('where')).toHaveTextContent('/session/ses_b?platform=r-B%3Aopencode');
    expect(wt.createAndLaunch).toHaveBeenCalledWith(expect.objectContaining({ projectDir: '/repo', remoteId: 'B' }));
    expect(seedNewSession).toHaveBeenCalledWith('ses_b', '/.worktrees/repo/x', 'r-B:opencode', 'x', 'B');
  });

  it('refuses to open for a disconnected owner instead of using this machine', async () => {
    openFor('gone');
    expect(await screen.findByText('Worktree sessions unavailable')).toBeInTheDocument();
    expect(askedFor).toEqual(['gone']);
    expect(wt.defaultBaseRef).not.toHaveBeenCalled();
  });
});
