// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { DraftSetupError } from './DraftSetupError';
import type { BackendConversationDraft } from '../../lib/backendConversationDrafts';

const record: BackendConversationDraft = {
  id: 'ocm_01234567-89ab-4cde-8fab-0123456789ab', metadata: { draftId: 'draft', directory: '/repo' },
  phase: 'failed', setupRetryable: true, steps: { worktree: 'error' }, createdAt: 1,
  error: 'Session setup failed. git worktree add: context deadline exceeded\nUpdating files: 99%',
  request: { directory: '/repo', send: { message: 'saved first message' } },
};

it('keeps the failure compact and offers a copy of the saved message', () => {
  render(<DraftSetupError record={record} retry={vi.fn()} />);
  expect(screen.getByRole('alert')).toHaveTextContent('Your submitted message is saved above.');
  expect(screen.getByRole('alert')).toHaveTextContent('git worktree add: context deadline exceeded');
  expect(screen.getByRole('alert')).not.toHaveTextContent('Updating files: 99%');
  expect(screen.getByRole('button', { name: 'Copy submitted message' })).toBeInTheDocument();
  const summary = screen.getByText('Error details');
  expect(summary.closest('details')).not.toHaveAttribute('open');
  expect(summary.closest('details')).toHaveTextContent('Updating files: 99%');
});

it('retains the message and error on retry failure and permits another explicit attempt', async () => {
  const retry = vi.fn().mockRejectedValueOnce(new Error('owner unavailable')).mockResolvedValueOnce(undefined);
  render(<DraftSetupError record={record} retry={retry} />);
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('owner unavailable'));
  expect(screen.getByRole('button', { name: 'Copy submitted message' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(retry).toHaveBeenCalledTimes(2));
  expect(screen.getByRole('alert')).toHaveTextContent('Your submitted message is saved above.');
});

it('does not submit duplicate retries while the acknowledgement is pending', async () => {
  let done!: () => void;
  const retry = vi.fn(() => new Promise<void>((resolve) => { done = resolve; }));
  render(<DraftSetupError record={record} retry={retry} />);
  const button = screen.getByRole('button', { name: 'Retry' });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(button).toBeDisabled();
  expect(retry).toHaveBeenCalledTimes(1);
  await act(async () => done());
  expect(button).not.toBeDisabled();
});

it('still retries first delivery on a confirmed native session', async () => {
  const retry = vi.fn().mockRejectedValueOnce('delivery unavailable');
  render(<DraftSetupError record={{ ...record, setupRetryable: false, result: { sessionId: record.id } as BackendConversationDraft['result'] }} retry={retry} />);
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('delivery unavailable'));
  expect(retry).toHaveBeenCalledOnce();
});

it('preserves access to saved text without replaying uncertain allocation', () => {
  render(<DraftSetupError record={{ ...record, setupRetryable: false }} retry={vi.fn()} />);
  expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
  expect(screen.getByRole('alert')).toHaveTextContent('Session creation may have started');
  expect(screen.getByRole('button', { name: 'Copy submitted message' })).toBeInTheDocument();
});

it('handles image-only submissions without offering an empty text copy', () => {
  render(<DraftSetupError record={{ ...record, request: undefined, error: undefined }} retry={vi.fn()} />);
  expect(screen.getByRole('alert')).toHaveTextContent('Session setup failed.');
  expect(screen.queryByRole('button', { name: 'Copy submitted message' })).toBeNull();
});
