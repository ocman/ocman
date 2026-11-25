// @vitest-environment jsdom
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { DraftSaveError } from './DraftSaveError';
import { loadBackendDraftConflict, resolveBackendDraftConflict, useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { getDraft, saveDraft } from '../../lib/composerDraft';

vi.mock('../../lib/backendConversationDrafts', async (original) => ({
  ...await original<typeof import('../../lib/backendConversationDrafts')>(),
  loadBackendDraftConflict: vi.fn(), resolveBackendDraftConflict: vi.fn(),
}));
const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
const server = { id, metadata: { draftId: id, directory: '/repo', text: 'server text' }, phase: 'draft' as const, createdAt: 1, steps: {} };
beforeEach(() => {
  vi.clearAllMocks();
  useBackendConversationDrafts.setState({ conflicts: { [id]: true } });
  vi.mocked(loadBackendDraftConflict).mockResolvedValue(server);
  vi.mocked(resolveBackendDraftConflict).mockResolvedValue(undefined);
  saveDraft(id, 'local text');
});

it('preserves the live composer on cancel and shows both versions only after Resolve', async () => {
  render(<DraftSaveError id={id} error="conflict" retry={vi.fn()} navigate={vi.fn()} getLocalText={() => 'latest typing'} />);
  expect(screen.queryByRole('dialog')).toBeNull();
  const warning = screen.getByRole('status');
  expect(warning).toHaveAttribute('data-tone', 'warning');
  fireEvent.click(within(warning).getByRole('button', { name: 'Resolve' }));
  const dialog = screen.getByRole('dialog');
  expect(within(dialog).getByRole('textbox', { name: 'My version' })).toHaveValue('latest typing');
  expect(await within(dialog).findByRole('textbox', { name: 'Server version' })).toHaveValue('server text');
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
  expect(getDraft(id)).toBe('latest typing');
  expect(resolveBackendDraftConflict).not.toHaveBeenCalled();
});

it.each(['Keep my edits', 'Use server version'])('requires an explicit %s choice', async (label) => {
  const navigate = vi.fn();
  render(<DraftSaveError id={id} error="conflict" retry={vi.fn()} navigate={navigate} />);
  fireEvent.click(screen.getByRole('button', { name: 'Resolve' }));
  const choice = await screen.findByRole('button', { name: label });
  await screen.findByRole('textbox', { name: 'Server version' });
  await act(async () => { fireEvent.click(choice); });
  expect(resolveBackendDraftConflict).toHaveBeenCalledWith(id, server, label === 'Keep my edits' ? 'local' : 'server');
  expect(navigate).toHaveBeenCalledTimes(label === 'Keep my edits' ? 0 : 1);
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('keeps local edits through load and resolution failures and reloads the server snapshot on Retry', async () => {
  vi.mocked(loadBackendDraftConflict).mockRejectedValueOnce(new Error('offline'));
  vi.mocked(resolveBackendDraftConflict).mockRejectedValueOnce(new Error('changed again'));
  render(<DraftSaveError id={id} error="conflict" retry={vi.fn()} navigate={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Resolve' }));
  const dialog = screen.getByRole('dialog');
  await within(dialog).findByText('offline');
  fireEvent.click(within(dialog).getByRole('button', { name: /Retry/ }));
  await within(dialog).findByRole('textbox', { name: 'Server version' });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Keep my edits' }));
  await within(dialog).findByText('changed again');
  expect(getDraft(id)).toBe('local text');
  fireEvent.click(within(dialog).getByRole('button', { name: /Retry/ }));
  await within(dialog).findByRole('textbox', { name: 'Server version' });
  expect(loadBackendDraftConflict).toHaveBeenCalledTimes(3);
});

it('resaves the live text on Resolve without opening a conflict dialog for a save failure', async () => {
  useBackendConversationDrafts.setState({ conflicts: {} });
  let fail!: (error: Error) => void;
  const retry = vi.fn(() => new Promise<void>((_, reject) => { fail = reject; }));
  render(<DraftSaveError id={id} error="offline" retry={retry} navigate={vi.fn()} getLocalText={() => 'latest typing'} />);
  const button = screen.getByRole('button', { name: 'Resolve' });
  fireEvent.click(button);
  expect(retry).toHaveBeenCalledOnce();
  expect(getDraft(id)).toBe('latest typing');
  expect(button).toBeDisabled();
  expect(screen.queryByRole('dialog')).toBeNull();
  await act(async () => fail(new Error('still offline')));
  expect(button).toBeEnabled();
  expect(screen.getByRole('status')).toHaveTextContent('offline');
});
