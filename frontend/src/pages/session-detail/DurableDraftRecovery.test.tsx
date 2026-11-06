// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { DurableDraftRecovery } from './DurableDraftRecovery';
import { refreshBackendDrafts } from '../../lib/backendConversationDrafts';

vi.mock('../../lib/backendConversationDrafts', async (original) => ({ ...await original<typeof import('../../lib/backendConversationDrafts')>(), refreshBackendDrafts: vi.fn() }));

it('does not mount an editable draft before delayed backend recovery finishes', async () => {
  let resolve!: () => void;
  vi.mocked(refreshBackendDrafts).mockReturnValue(new Promise<void>((done) => { resolve = done; }));
  const mounted = vi.fn();
  function Editor() { mounted(); return <div>Editable prompt</div>; }
  render(<DurableDraftRecovery params={{ directory: '/repo' }}><Editor /></DurableDraftRecovery>);
  expect(mounted).not.toHaveBeenCalled();
  expect(screen.queryByText('Editable prompt')).toBeNull();
  await act(async () => { resolve(); });
  expect(screen.getByText('Editable prompt')).toBeInTheDocument();
});

it('keeps editing blocked after recovery fails and supports an explicit retry', async () => {
  vi.mocked(refreshBackendDrafts).mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(undefined);
  render(<DurableDraftRecovery params={{ directory: '/repo' }}><div>Editable prompt</div></DurableDraftRecovery>);
  await screen.findByText('offline');
  expect(screen.queryByText('Editable prompt')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(await screen.findByText('Editable prompt')).toBeInTheDocument();
});
