// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RenameModal } from './RenameModal';

const renameSession = vi.fn();
vi.mock('../../lib/api', () => ({ api: { renameSession: (...args: unknown[]) => renameSession(...args) } }));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));

describe('RenameModal', () => {
  it('stays open and busy until the rename is saved, and submits once', async () => {
    let finish!: () => void;
    renameSession.mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
    const onClose = vi.fn();
    const onRenamed = vi.fn();
    render(<RenameModal sessionId="s1" initialTitle="old" onClose={onClose} onRenamed={onRenamed} />);

    await userEvent.clear(screen.getByPlaceholderText('Session title'));
    await userEvent.type(screen.getByPlaceholderText('Session title'), 'new{Enter}{Enter}');
    const busy = screen.getByRole('button', { name: 'Renaming…' });
    expect(busy).toHaveAttribute('aria-busy', 'true');
    await userEvent.click(busy);
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    await userEvent.keyboard('{Escape}');
    expect(renameSession).toHaveBeenCalledOnce();
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => { finish(); });
    expect(onRenamed).toHaveBeenCalledWith('new');
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('stays open when the rename fails', async () => {
    renameSession.mockRejectedValue(new Error('boom'));
    const onClose = vi.fn();
    render(<RenameModal sessionId="s1" initialTitle="old" onClose={onClose} onRenamed={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: 'Rename' }));
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Rename' })).toBeEnabled();
  });
});
