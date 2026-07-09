// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { WorktreeFormModal } from './WorktreeFormModal';

// The modal's inherit hint (#101) reads the setting + the parent's
// approved-permissions count. Mock the API and force the capability on.
// uiStore is mocked to a plain selector so the persist middleware
// doesn't need a real localStorage.
let uiState: Record<string, unknown> = {};
vi.mock('../lib/uiStore', () => ({
  useUiStore: (sel: (s: Record<string, unknown>) => unknown) => sel(uiState),
}));
vi.mock('../lib/api', () => ({
  api: {
    getWorktreeInheritPermissions: vi.fn(),
    approvedPermissions: vi.fn(),
    worktree: { defaultBaseRef: vi.fn().mockResolvedValue({ baseRef: 'main' }) },
  },
}));
vi.mock('../lib/useCapabilities', () => ({
  useWorktreeSessions: () => true,
}));
vi.mock('../lib/apiStore', () => ({
  useApiStore: (sel: (s: Record<string, unknown>) => unknown) =>
    sel({ getProjects: vi.fn().mockResolvedValue([]), seedNewSession: vi.fn() }),
}));

import { api } from '../lib/api';
const m = api as unknown as {
  getWorktreeInheritPermissions: ReturnType<typeof vi.fn>;
  approvedPermissions: ReturnType<typeof vi.fn>;
};

afterEach(() => {
  vi.clearAllMocks();
});

function openWith(parentSessionId?: string) {
  uiState = {
    worktreeFormOpen: true,
    worktreeFormGen: 1,
    worktreeFormProject: '/repo',
    worktreeFormBranch: undefined,
    worktreeFormParentSessionId: parentSessionId,
    closeWorktreeForm: vi.fn(),
  };
  return render(
    <MemoryRouter>
      <WorktreeFormModal />
    </MemoryRouter>,
  );
}

describe('WorktreeFormModal inherit hint (#101)', () => {
  it('shows the count when a parent has approvals and the setting is on', async () => {
    m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: true });
    m.approvedPermissions.mockResolvedValue([{}, {}, {}]);
    openWith('ses_parent');
    const hint = await screen.findByTestId('worktree-inherit-hint');
    expect(hint).toHaveTextContent('Will inherit 3 approved permissions');
  });

  it('uses singular wording for a single permission', async () => {
    m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: true });
    m.approvedPermissions.mockResolvedValue([{}]);
    openWith('ses_parent');
    const hint = await screen.findByTestId('worktree-inherit-hint');
    expect(hint).toHaveTextContent('Will inherit 1 approved permission');
    expect(hint).not.toHaveTextContent('permissions');
  });

  it('hides the hint when the setting is off', async () => {
    m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: false });
    m.approvedPermissions.mockResolvedValue([{}, {}]);
    openWith('ses_parent');
    // Let effects settle.
    await waitFor(() => expect(m.getWorktreeInheritPermissions).toHaveBeenCalled());
    expect(screen.queryByTestId('worktree-inherit-hint')).toBeNull();
  });

  it('hides the hint when there is no parent session', async () => {
    openWith(undefined);
    await waitFor(() => expect(screen.getByText('New worktree session')).toBeInTheDocument());
    expect(m.getWorktreeInheritPermissions).not.toHaveBeenCalled();
    expect(screen.queryByTestId('worktree-inherit-hint')).toBeNull();
  });

  it('hides the hint when the parent has zero approvals', async () => {
    m.getWorktreeInheritPermissions.mockResolvedValue({ enabled: true });
    m.approvedPermissions.mockResolvedValue([]);
    openWith('ses_parent');
    await waitFor(() => expect(m.approvedPermissions).toHaveBeenCalled());
    expect(screen.queryByTestId('worktree-inherit-hint')).toBeNull();
  });
});
