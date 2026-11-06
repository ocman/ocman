// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { screen, waitFor } from '@testing-library/react';
import { renderWithQuery } from '../test/renderWithQuery';
import userEvent from '@testing-library/user-event';
import { api, type Routine } from '../lib/api';
import { RoutineEditorForm } from './RoutineEditorDrawer';

vi.mock('../lib/api', () => ({ api: { projects: vi.fn(), sessions: vi.fn(), agents: vi.fn(), sessionModels: vi.fn(), prepareSession: vi.fn(), routines: { create: vi.fn(), update: vi.fn() }, webhookInboxes: { subscribe: vi.fn(), unsubscribe: vi.fn() } } }));
vi.mock('../lib/useCapabilities', () => ({ useMachinePlatforms: () => [] }));

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
  vi.mocked(api.prepareSession).mockResolvedValue({ agents: [], commands: [], models: { models: [] } } as never);
  vi.mocked(api.routines.update).mockResolvedValue(routine);
});

function editor(value = routine) {
  renderWithQuery(<RoutineEditorForm routine={value} inboxes={[]} onClose={vi.fn()} onSaved={vi.fn()} onRefresh={vi.fn()} />);
}

it('configures a webhook project field for an unbound routine', async () => {
  const inboxes = [{ id: 'i', name: 'PRs', subscriptions: [] }] as never;
  renderWithQuery(<RoutineEditorForm routine={{ ...routine, directory: '' }} inboxes={inboxes} onClose={vi.fn()} onSaved={vi.fn()} onRefresh={vi.fn()} />);
  await userEvent.selectOptions(screen.getByLabelText('Trigger'), 'webhook:i');
  const field = screen.getByLabelText('Webhook project field');
  expect(field).toBeRequired();
  await userEvent.type(field, '/repository/clone_url');
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }));
  await waitFor(() => expect(api.webhookInboxes.subscribe).toHaveBeenCalledWith('i', expect.objectContaining({ projectPointer: '/repository/clone_url' })));
});

it('saves a projectless routine without preparing a runtime', async () => {
  editor({ ...routine, directory: '' });
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }));
  await waitFor(() => expect(api.routines.update).toHaveBeenCalledWith('r', expect.objectContaining({ directory: '', sessionMode: 'new', schedule: { kind: 'none' } })));
  expect(api.prepareSession).not.toHaveBeenCalled();
  await userEvent.click(screen.getByText('Session and model'));
  expect(screen.getByRole('option', { name: 'Reuse session' })).toBeDisabled();
  expect(screen.getByRole('option', { name: 'Existing session' })).toBeDisabled();
  expect(screen.getByRole('option', { name: 'Cron' })).toBeDisabled();
});

it('clears the project and shared session without changing the owning machine', async () => {
  editor({ ...routine, remoteId: 'build-box', sessionMode: 'reuse', sessionId: 'previous', scheduleKind: 'cron', scheduleConfigJSON: '{"cron":"0 9 * * *"}' });
  await userEvent.click(screen.getByRole('combobox', { name: 'Project' }));
  await userEvent.click(screen.getByRole('option', { name: 'Choose when running' }));
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }));
  await waitFor(() => expect(api.routines.update).toHaveBeenCalledWith('r', expect.objectContaining({ directory: '', remoteId: 'build-box', sessionMode: 'new', sessionId: '', schedule: { kind: 'none' } })));
});

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
