// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

const openWorktreeForm = vi.fn();
vi.mock('../../lib/uiStore', () => ({
  useUiStore: (selector: (s: { openWorktreeForm: typeof openWorktreeForm }) => unknown) =>
    selector({ openWorktreeForm }),
}));

const gitBranches = vi.fn();
vi.mock('../../lib/api', () => ({
  api: {
    gitBranches: (...a: unknown[]) => gitBranches(...a),
  },
}));

import { TargetSelector } from './ComposerSelectorRow';

beforeEach(() => {
  openWorktreeForm.mockReset();
  gitBranches.mockReset().mockResolvedValue({ branches: ['main', 'feature/x'] });
});
afterEach(() => vi.clearAllMocks());

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
    expect(openWorktreeForm).toHaveBeenCalledWith({ projectDir: '/a', branch: 'main', parentSessionId: undefined });
  });

  it('forwards the current session as parentSessionId (#101)', async () => {
    render(<TargetSelector directory="/a" worktreesSupported parentSessionId="ses_1" />);
    const target = await screen.findByLabelText('Session target');
    fireEvent.change(target, { target: { value: 'worktree' } });
    expect(openWorktreeForm).toHaveBeenCalledWith({ projectDir: '/a', branch: 'main', parentSessionId: 'ses_1' });
  });

  it('hides the worktree option when worktrees are unsupported', async () => {
    render(<TargetSelector directory="/a" worktreesSupported={false} />);
    await screen.findByLabelText('Session target');
    expect(screen.queryByRole('option', { name: 'New worktree…' })).not.toBeInTheDocument();
  });
});
