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
    expect(screen.getByRole('combobox')).toHaveTextContent('New worktree');
    await userEvent.click(screen.getByRole('combobox'));
    await userEvent.click(screen.getByRole('option', { name: 'Current checkout' }));
    expect(onTargetChange).toHaveBeenCalledWith('current');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('offers existing worktrees while keeping a new worktree as the default', async () => {
    const onTargetChange = vi.fn();
    render(<TargetSelector directory="/a" worktreesSupported onTargetChange={onTargetChange}
      worktrees={[{ path: '/wt/feat', branch: 'feat' }, { path: '/wt/detached', branch: '' }]} />);
    expect(screen.getByRole('combobox')).toHaveTextContent('New worktree');
    await userEvent.click(screen.getByRole('combobox'));
    expect(screen.getByRole('option', { name: 'Worktree detached' })).toBeInTheDocument();
    await userEvent.type(screen.getByRole('textbox', { name: 'Search worktrees' }), 'feat');
    expect(screen.queryByRole('option', { name: 'Worktree detached' })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('option', { name: 'Worktree feat' }));
    expect(onTargetChange).toHaveBeenCalledWith('dir:/wt/feat');
  });

  it('only offers current checkout when worktrees are unavailable', async () => {
    render(<TargetSelector directory="/a" worktreesSupported={false} worktrees={[{ path: '/wt/feat', branch: 'feat' }]} />);
    expect(screen.getByRole('combobox')).toHaveTextContent('Current checkout');
    await userEvent.click(screen.getByRole('combobox'));
    expect(screen.queryByRole('option', { name: 'Worktree feat' })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'New worktree' })).not.toBeInTheDocument();
  });
});
