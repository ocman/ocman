// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithQuery } from '../test/renderWithQuery';
import { api, type Routine } from '../lib/api';
import { RoutineRunDialog } from './RoutineRunDialog';

vi.mock('../lib/api', () => ({ api: { projects: vi.fn(), routines: { run: vi.fn() } } }));
const routine = { id: 'r', name: 'Review', directory: '', remoteId: 'local' } as Routine;

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(api.projects).mockResolvedValue([
    { directory: '/repo' }, { directory: '/remote', remoteId: 'remote' }, { directory: '/archived', archived: true },
  ] as never);
  vi.mocked(api.routines.run).mockResolvedValue({} as never);
});

it('requires a project on the routine owner and submits it', async () => {
  const onStarted = vi.fn();
  renderWithQuery(<RoutineRunDialog routine={routine} onClose={vi.fn()} onStarted={onStarted} />);
  expect(screen.getByRole('button', { name: 'Run now' })).toBeDisabled();
  await userEvent.click(screen.getByRole('combobox', { name: 'Run project' }));
  await userEvent.click(await screen.findByRole('option', { name: '/repo' }));
  expect(screen.queryByRole('option', { name: '/remote' })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: 'Run now' }));
  await waitFor(() => expect(onStarted).toHaveBeenCalledOnce());
  expect(api.routines.run).toHaveBeenCalledWith('r', '/repo');
});

it('shows launch failures and lets the user retry or cancel', async () => {
  vi.mocked(api.routines.run).mockRejectedValueOnce(new Error('Owner unavailable'));
  const onClose = vi.fn();
  renderWithQuery(<RoutineRunDialog routine={routine} onClose={onClose} onStarted={vi.fn()} />);
  await userEvent.click(screen.getByRole('combobox', { name: 'Run project' }));
  await userEvent.click(await screen.findByRole('option', { name: '/repo' }));
  await userEvent.click(screen.getByRole('button', { name: 'Run now' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Owner unavailable');
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(onClose).toHaveBeenCalledOnce();
});
