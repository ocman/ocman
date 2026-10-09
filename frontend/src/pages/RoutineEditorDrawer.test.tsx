// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { api, type Routine } from '../lib/api';
import { RoutineEditorForm } from './RoutineEditorDrawer';

vi.mock('../lib/api', () => ({ api: { projects: vi.fn(), sessions: vi.fn(), agents: vi.fn(), sessionModels: vi.fn(), routines: { create: vi.fn(), update: vi.fn() } } }));

const routine: Routine = {
  id: 'r', name: 'Check', prompt: 'Inspect', directory: '/repo', remoteId: 'local', agent: '', model: '',
  sessionMode: 'new', sessionId: '', scheduleKind: 'none', scheduleConfigJSON: '{}', permissionRulesJSON: '[]',
  nextDueAt: 0, enabled: true, deleted: false, deleteAfterSuccess: false, archiveSessionAfterSuccess: false,
  notifyOnSuccess: false, createdAt: 1, updatedAt: 1,
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.projects).mockResolvedValue([]);
  vi.mocked(api.sessions).mockResolvedValue([]);
  vi.mocked(api.routines.update).mockResolvedValue(routine);
});

function editor(value = routine) {
  render(<RoutineEditorForm routine={value} inboxes={[]} onClose={vi.fn()} onSaved={vi.fn()} onRefresh={vi.fn()} />);
}

it('defaults to the current checkout and saves an explicit cleanup opt-in', async () => {
  const user = userEvent.setup();
  editor();
  await user.click(screen.getByText('Session and model'));
  expect(screen.getByLabelText('Workspace')).toHaveValue('local');
  expect(screen.queryByRole('checkbox', { name: 'Clean up worktree after a successful run' })).not.toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText('Workspace'), 'worktree');
  const cleanup = screen.getByRole('checkbox', { name: 'Clean up worktree after a successful run' });
  expect(cleanup).not.toBeChecked();
  await user.click(cleanup);
  await user.click(screen.getByRole('button', { name: 'Save changes' }));
  await waitFor(() => expect(api.routines.update).toHaveBeenCalledWith('r', expect.objectContaining({ worktree: true, cleanupWorktree: true })));
});

it('loads worktree settings and clears them when switching to reused sessions', async () => {
  const user = userEvent.setup();
  editor({ ...routine, worktree: true, cleanupWorktree: true });
  await user.click(screen.getByText('Session and model'));
  expect(screen.getByLabelText('Workspace')).toHaveValue('worktree');
  expect(screen.getByRole('checkbox', { name: 'Clean up worktree after a successful run' })).toBeChecked();
  await user.selectOptions(screen.getByLabelText('Session'), 'reuse');
  expect(screen.queryByLabelText('Workspace')).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Save changes' }));
  await waitFor(() => expect(api.routines.update).toHaveBeenCalledWith('r', expect.objectContaining({ sessionMode: 'reuse', worktree: false, cleanupWorktree: false })));
});
