// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

const openWorktreeForm = vi.fn();
vi.mock('../../lib/uiStore', () => ({
  useUiStore: (selector: (s: { openWorktreeForm: typeof openWorktreeForm }) => unknown) =>
    selector({ openWorktreeForm }),
}));

const gitBranches = vi.fn();
const gitCheckout = vi.fn();
vi.mock('../../lib/api', () => ({
  api: {
    gitBranches: (...a: unknown[]) => gitBranches(...a),
    gitCheckout: (...a: unknown[]) => gitCheckout(...a),
  },
}));

import { BranchSelector, TargetSelector } from './ComposerSelectorRow';

beforeEach(() => {
  openWorktreeForm.mockReset();
  gitBranches.mockReset().mockResolvedValue({ branches: ['main', 'feature/x'] });
  gitCheckout.mockReset().mockResolvedValue({ branch: 'feature/x' });
});
afterEach(() => vi.clearAllMocks());

describe('BranchSelector', () => {
  it('renders nothing without a directory', () => {
    const { container } = render(<BranchSelector directory={undefined} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders branch options with current first', async () => {
    render(<BranchSelector directory="/a" />);
    const branchSelect = await screen.findByLabelText('Git branch');
    expect((branchSelect as HTMLSelectElement).value).toBe('main');
    expect(screen.getByRole('option', { name: 'feature/x' })).toBeInTheDocument();
  });

  it('calls gitCheckout when a branch is picked', async () => {
    render(<BranchSelector directory="/a" />);
    const branchSelect = await screen.findByLabelText('Git branch');
    fireEvent.change(branchSelect, { target: { value: 'feature/x' } });
    await waitFor(() => expect(gitCheckout).toHaveBeenCalledWith('/a', 'feature/x'));
  });

  it('surfaces a checkout error inline', async () => {
    gitCheckout.mockRejectedValue(new Error('would overwrite local changes'));
    render(<BranchSelector directory="/a" />);
    const branchSelect = await screen.findByLabelText('Git branch');
    fireEvent.change(branchSelect, { target: { value: 'feature/x' } });
    expect(await screen.findByRole('alert')).toHaveTextContent(/overwrite/);
  });
});

describe('TargetSelector', () => {
  it('renders nothing without a directory', () => {
    const { container } = render(
      <TargetSelector directory={undefined} worktreesSupported />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('opens the worktree form when "New worktree" is chosen', async () => {
    render(<TargetSelector directory="/a" worktreesSupported />);
    const target = await screen.findByLabelText('Session target');
    fireEvent.change(target, { target: { value: 'worktree' } });
    expect(openWorktreeForm).toHaveBeenCalledWith({ projectDir: '/a', branch: 'main' });
  });

  it('hides the worktree option when worktrees are unsupported', async () => {
    render(<TargetSelector directory="/a" worktreesSupported={false} />);
    await screen.findByLabelText('Session target');
    expect(screen.queryByRole('option', { name: 'New worktree…' })).not.toBeInTheDocument();
  });
});
