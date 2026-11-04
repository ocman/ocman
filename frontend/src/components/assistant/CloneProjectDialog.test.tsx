// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { postJSON } from '../../lib/api';
import { CloneProjectDialog } from './CloneProjectDialog';

vi.mock('../../lib/api', () => ({ postJSON: vi.fn() }));
const machine = { remoteId: 'box', remoteName: 'Build box', platform: 'r-box:opencode', dir: '' };
beforeEach(() => { vi.mocked(postJSON).mockReset(); });

it('accepts a manual repository and uses the owner-validated destination', async () => {
  const onSelect = vi.fn().mockResolvedValue(undefined);
  const onClose = vi.fn();
  vi.mocked(postJSON).mockResolvedValue({ url: 'ssh://git@example.com/Repo.git', directory: '/owner/src/Repo' });
  render(<CloneProjectDialog machine={machine} onSelect={onSelect} onClose={onClose} />);
  expect(screen.getByRole('button', { name: 'Use machine and clone on submit' })).toBeDisabled();
  expect(postJSON).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText('Repository URL'), { target: { value: 'git@example.com:Repo.git' } });
  fireEvent.click(screen.getByRole('button', { name: 'Use machine and clone on submit' }));
  await waitFor(() => expect(onSelect).toHaveBeenCalledWith({ ...machine, dir: '/owner/src/Repo', cloneUrl: 'ssh://git@example.com/Repo.git' }));
  expect(onClose).toHaveBeenCalledOnce();
});

it('locks confirmation and closing while validating and keeps selection failures retryable', async () => {
  let finish!: (value: unknown) => void;
  vi.mocked(postJSON).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
  const onSelect = vi.fn().mockRejectedValue(new Error('Draft storage is full'));
  const onClose = vi.fn();
  render(<CloneProjectDialog machine={machine} onSelect={onSelect} onClose={onClose} />);
  fireEvent.change(screen.getByLabelText('Repository URL'), { target: { value: 'https://example.com/repo' } });
  fireEvent.click(screen.getByRole('button', { name: 'Use machine and clone on submit' }));
  expect(screen.getByRole('button', { name: 'Checking destination…' })).toBeDisabled();
  expect(screen.getByRole('button', { name: /^Cancel$/ })).toBeDisabled();
  fireEvent.keyDown(window, { key: 'Escape' });
  expect(onClose).not.toHaveBeenCalled();
  await act(async () => finish({ url: 'https://example.com/repo', directory: '/repo' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Draft storage is full');
  expect(screen.getByRole('button', { name: 'Use machine and clone on submit' })).not.toBeDisabled();
});

it('aborts suggestion loading when cancelled and ignores a late response', async () => {
  let finish!: (value: unknown) => void;
  vi.mocked(postJSON).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
  const onSelect = vi.fn();
  const view = render(<CloneProjectDialog machine={machine} suggestedURL="https://example.com/repo" onSelect={onSelect} onClose={vi.fn()} />);
  const signal = vi.mocked(postJSON).mock.calls[0][2]?.signal;
  expect(screen.getByRole('status')).toHaveTextContent('Checking destination');
  view.unmount();
  expect(signal?.aborted).toBe(true);
  await act(async () => finish({ url: 'https://example.com/repo', directory: '/repo' }));
  expect(onSelect).not.toHaveBeenCalled();
});
