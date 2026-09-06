// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { api, type Routine } from '../lib/api';
import { resolveTargetForDir } from '../lib/machinePicker';
import { Routines } from './Routines';

vi.mock('../lib/headerContext', () => ({ usePageTitle: vi.fn() }));
vi.mock('../lib/machinePicker', () => ({ resolveTargetForDir: vi.fn() }));
vi.mock('../lib/api', () => ({ api: { projects: vi.fn(), routines: { list: vi.fn(), create: vi.fn(), update: vi.fn(), remove: vi.fn(), run: vi.fn(), history: vi.fn() } } }));

const routine: Routine = {
  id: 'routine-1', name: 'Morning check', prompt: 'Inspect the build', directory: '/repo', remoteId: 'local',
  scheduleKind: 'cron', scheduleConfigJSON: '{"cron":"0 9 * * *","timezone":"Europe/Brussels"}', nextDueAt: 2_000_000,
  enabled: true, deleted: false, deleteAfterSuccess: false, createdAt: 1_000, updatedAt: 1_000,
};

describe('Routines', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.projects).mockResolvedValue([{ directory: '/repo', archived: false } as never]);
    vi.mocked(api.routines.list).mockResolvedValue([routine]);
    vi.mocked(api.routines.history).mockResolvedValue([{ id: 'run-1', routineId: routine.id, routineUpdatedAt: routine.updatedAt, routineName: routine.name, prompt: routine.prompt, directory: '/repo', remoteId: 'local', trigger: 'manual', platform: 'opencode', sessionId: 'session-1', state: 'failure', error: 'agent stopped', occurrenceAt: 1_000, createdAt: 1_000 }]);
    vi.mocked(resolveTargetForDir).mockResolvedValue({ platform: 'r-box:opencode', remoteId: 'box' });
    vi.mocked(api.routines.create).mockResolvedValue(routine);
    vi.mocked(api.routines.update).mockResolvedValue(routine);
    vi.mocked(api.routines.remove).mockResolvedValue(undefined);
    vi.mocked(api.routines.run).mockResolvedValue({} as never);
  });

  afterEach(() => vi.useRealTimers());

  it('creates a targeted timeout routine and exposes every schedule form', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await screen.findByRole('heading', { name: 'Morning check' });
    await user.click(screen.getByRole('button', { name: 'New routine' }));
    await user.type(screen.getByLabelText('Name'), 'Deploy check');
    await user.type(screen.getByLabelText('Prompt'), 'Check production');
    await user.click(screen.getByRole('combobox', { name: 'Project' }));
    await user.click(screen.getByRole('option', { name: '/repo' }));
    await user.selectOptions(screen.getByLabelText('Schedule'), 'once');
    expect(screen.getByLabelText('Run at')).toHaveAttribute('type', 'datetime-local');
    await user.selectOptions(screen.getByLabelText('Schedule'), 'cron');
    expect(screen.getByLabelText('Timezone')).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText('Schedule'), 'timeout');
    await user.clear(screen.getByLabelText('Minutes from now'));
    await user.type(screen.getByLabelText('Minutes from now'), '15');
    await user.click(screen.getByLabelText('Delete after a successful run'));
    await user.click(screen.getByRole('button', { name: 'Create routine' }));

    await waitFor(() => expect(api.routines.create).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Deploy check', directory: '/repo', remoteId: 'box', deleteAfterSuccess: true,
      schedule: { kind: 'timeout', timeoutMs: 900_000 },
    })));
  }, 15_000);

  it('edits, runs, deletes, and links routine history sessions', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    const card = (await screen.findByRole('heading', { name: routine.name })).closest('article')!;
    expect(within(card).getByRole('alert')).toHaveTextContent('agent stopped');
    await user.click(within(card).getByText('History (1)'));
    expect(within(card).getByRole('link', { name: 'Open session' })).toHaveAttribute('href', '/session/session-1?platform=opencode');
    await user.click(within(card).getByRole('button', { name: 'Run now' }));
    await waitFor(() => expect(api.routines.run).toHaveBeenCalledWith(routine.id));
    await user.click(within(card).getByRole('button', { name: 'Edit' }));
    expect(screen.getByLabelText('Cron expression')).toHaveValue('0 9 * * *');
    await user.clear(screen.getByLabelText('Name'));
    await user.type(screen.getByLabelText('Name'), 'Renamed');
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(api.routines.update).toHaveBeenCalledWith(routine.id, expect.objectContaining({ name: 'Renamed' })));
    await user.click(within(card).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(api.routines.remove).toHaveBeenCalledWith(routine.id));
  });

  it('surfaces load and action errors', async () => {
    vi.mocked(api.routines.list).mockRejectedValueOnce(new Error('load failed'));
    const { unmount } = render(<MemoryRouter><Routines /></MemoryRouter>);
    expect(await screen.findByRole('alert')).toHaveTextContent('load failed');
    unmount();

    vi.mocked(api.routines.list).mockResolvedValue([routine]);
    vi.mocked(api.routines.run).mockRejectedValueOnce(new Error('run failed'));
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await user.click(await screen.findByRole('button', { name: 'Run now' }));
    expect(await screen.findByText('run failed')).toBeInTheDocument();
  });

  it('rejects target fallback but preserves an explicit local target', async () => {
    const user = userEvent.setup();
    vi.mocked(resolveTargetForDir).mockResolvedValue({ platform: '' });
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await screen.findByRole('heading', { name: routine.name });
    await user.click(screen.getByRole('button', { name: 'Edit' }));
    await user.click(screen.getByRole('button', { name: 'Save changes' }));

    expect(await screen.findByText('Could not resolve routine target.')).toBeInTheDocument();
    expect(api.routines.update).not.toHaveBeenCalled();

    vi.mocked(resolveTargetForDir).mockResolvedValue({ platform: 'opencode', remoteId: 'local' });
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(api.routines.update).toHaveBeenCalledWith(routine.id, expect.objectContaining({ remoteId: 'local' })));
  });

  it('refreshes routines and history every five seconds while mounted', async () => {
    vi.useFakeTimers();
    const { unmount } = render(<MemoryRouter><Routines /></MemoryRouter>);
    await act(async () => {});
    expect(api.routines.list).toHaveBeenCalledTimes(1);

    vi.mocked(api.routines.history).mockResolvedValue([{ id: 'run-2', routineId: routine.id, routineUpdatedAt: routine.updatedAt, routineName: routine.name, prompt: routine.prompt, directory: '/repo', remoteId: 'local', trigger: 'schedule', state: 'success', occurrenceAt: 2_000, createdAt: 2_000 }]);
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });

    expect(api.routines.list).toHaveBeenCalledTimes(2);
    expect(screen.getByText('success')).toBeInTheDocument();
    unmount();
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
    expect(api.routines.list).toHaveBeenCalledTimes(2);
  });
});
