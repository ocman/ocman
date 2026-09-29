// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SubmitButton } from './Control';
import { remoteLog } from '../lib/remoteLog';

vi.mock('../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));

function deferred() {
  let resolve!: () => void;
  let reject!: (err: Error) => void;
  const promise = new Promise<void>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

describe('SubmitButton', () => {
  it('stays busy and disabled until the action settles, then runs the continuation', async () => {
    const work = deferred();
    const after = vi.fn();
    const onClick = vi.fn(async () => { await work.promise; after(); });
    render(<SubmitButton onClick={onClick} pendingLabel="Saving…">Save</SubmitButton>);

    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    const busy = screen.getByRole('button', { name: 'Saving…' });
    expect(busy).toBeDisabled();
    expect(busy).toHaveAttribute('aria-busy', 'true');
    expect(after).not.toHaveBeenCalled();

    await act(async () => { work.resolve(); await work.promise; });
    const idle = screen.getByRole('button', { name: 'Save' });
    expect(idle).toBeEnabled();
    expect(idle).toHaveAttribute('aria-busy', 'false');
    expect(after).toHaveBeenCalledOnce();
  });

  it('ignores repeat clicks while running', async () => {
    const work = deferred();
    const onClick = vi.fn(() => work.promise);
    render(<SubmitButton onClick={onClick}>Save</SubmitButton>);
    const button = screen.getByRole('button', { name: 'Save' });
    act(() => { button.click(); button.click(); });
    expect(onClick).toHaveBeenCalledOnce();
    await act(async () => { work.resolve(); await work.promise; });
  });

  it('recovers and logs after a rejected action', async () => {
    const err = new Error('boom');
    render(<SubmitButton onClick={() => Promise.reject(err)}>Save</SubmitButton>);
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
    expect(remoteLog.error).toHaveBeenCalledWith('Button action failed', err);
  });

  it('shows the caller-owned pending state and blocks clicks', async () => {
    const onClick = vi.fn();
    render(<SubmitButton pending onClick={onClick}>Save</SubmitButton>);
    const button = screen.getByRole('button', { name: 'Save' });
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(button).toBeDisabled();
    expect(onClick).not.toHaveBeenCalled();
  });
});
