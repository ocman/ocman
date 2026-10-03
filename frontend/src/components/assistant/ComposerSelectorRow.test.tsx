// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { TargetSelector } from './ComposerSelectorRow';

describe('TargetSelector', () => {
  it('hides without a directory', () => {
    render(<TargetSelector worktreesSupported />);
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  });

  it('defaults to a new worktree without opening a form', async () => {
    const onTargetChange = vi.fn();
    render(<TargetSelector directory="/a" worktreesSupported onTargetChange={onTargetChange} />);
    expect(screen.getByRole('combobox')).toHaveValue('worktree');
    await userEvent.selectOptions(screen.getByRole('combobox'), 'current');
    expect(onTargetChange).toHaveBeenCalledWith('current');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('offers existing worktrees while keeping a new worktree as the default', async () => {
    const onTargetChange = vi.fn();
    render(<TargetSelector directory="/a" worktreesSupported onTargetChange={onTargetChange}
      worktrees={[{ path: '/wt/feat', branch: 'feat' }, { path: '/wt/detached', branch: '' }]} />);
    expect(screen.getByRole('combobox')).toHaveValue('worktree');
    expect(screen.getByRole('option', { name: 'Worktree detached' })).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByRole('combobox'), 'Worktree feat');
    expect(onTargetChange).toHaveBeenCalledWith('dir:/wt/feat');
  });

  it('only offers current checkout when worktrees are unavailable', () => {
    render(<TargetSelector directory="/a" worktreesSupported={false} worktrees={[{ path: '/wt/feat', branch: 'feat' }]} />);
    expect(screen.queryByRole('option', { name: 'Worktree feat' })).not.toBeInTheDocument();
    expect(screen.getByRole('combobox')).toHaveValue('current');
    expect(screen.queryByRole('option', { name: 'New worktree' })).not.toBeInTheDocument();
  });
});
