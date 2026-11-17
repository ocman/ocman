// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { renderWithQuery } from '../test/renderWithQuery';
import { backgroundTasks, type BackgroundTask } from '../lib/backgroundTasks';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { BackgroundTasks } from './BackgroundTasks';

vi.mock('../lib/backgroundTasks', () => ({ backgroundTasks: vi.fn() }));
vi.mock('../lib/settingsQueries', () => ({ useRemotes: () => ({ data: [] }) }));
vi.mock('../lib/usePanelVisible', () => ({ useDocumentVisible: vi.fn(() => true) }));

function renderTasks(url = '/') {
  return renderWithQuery(<MemoryRouter initialEntries={[url]}><BackgroundTasks /></MemoryRouter>);
}

function task(id: string, overrides: Partial<BackgroundTask> = {}): BackgroundTask {
  return { id, title: `Task ${id}`, kind: 'conversation.history', ownerId: 'local', status: 'running',
    progress: { phase: 'checking sessions', completed: 3, total: 5, unit: 'sessions', counters: { imported: 2, unchanged: 1 } },
    createdAt: 1000, updatedAt: 2000, ...overrides };
}

describe('Background task table', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(useDocumentVisible).mockReturnValue(true);
  });

  it('groups task runs and exposes newest-first history only in the selected detail panel', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([
      task('old', { key: 'scan', title: 'History scan', status: 'failed', error: 'Previous scan failed' }),
      task('new', { key: 'scan', title: 'History scan', createdAt: 3000 }),
      task('other'),
    ]);
    renderTasks();
    const table = await screen.findByRole('table', { name: 'Background task groups' });
    expect(within(table).getAllByRole('button')).toHaveLength(2);
    const toggle = within(table).getByRole('button', { name: 'History scan' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByText('Previous scan failed')).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    const details = screen.getByRole('region', { name: 'Task group details for History scan' });
    expect(details).toHaveAttribute('id', toggle.getAttribute('aria-controls'));
    expect(within(details).getByText('2 runs')).toBeVisible();
    const history = within(details).getByRole('region', { name: 'History for History scan' });
    const runs = within(history).getAllByRole('listitem');
    expect(runs).toHaveLength(2);
    expect(runs[0]).toHaveTextContent('Running');
    fireEvent.click(within(runs[1]).getByText(/Failed/));
    expect(within(runs[1]).getByText('Previous scan failed')).toBeVisible();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(screen.queryByRole('region', { name: 'Task group details for History scan' })).not.toBeInTheDocument();
  });

  it('shows active, queued, failed, cancelled and interrupted results without exposing progress in the table', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([
      task('running'), task('queued', { status: 'queued', progress: { phase: 'queued', completed: 0, total: null, counters: {} } }),
      task('failed', { status: 'failed', error: 'History was not fully verified', progress: { phase: 'checking', completed: 5, total: 5, unit: 'sessions', counters: { failed: 2 } } }),
      task('interrupted', { status: 'interrupted', error: 'Work was not replayed.' }), task('cancelled', { status: 'cancelled' }),
    ]);
    renderTasks();
    const table = await screen.findByRole('table');
    expect(within(table).getAllByRole('button')).toHaveLength(5);
    expect(within(table).queryByRole('progressbar')).not.toBeInTheDocument();
    expect(within(table).getByText('Cancelled')).toBeVisible();
    expect(within(table).getByText('Interrupted')).toBeVisible();
    const running = within(table).getByRole('button', { name: 'Task running' }).closest('tr')!;
    expect(running.querySelector('.oc-loading-spinner')).toHaveAttribute('aria-hidden', 'true');
    const queued = within(table).getByRole('button', { name: 'Task queued' }).closest('tr')!;
    expect(queued.querySelector('.oc-loading-spinner')).toBeNull();
    fireEvent.click(within(table).getByRole('button', { name: 'Task running' }));
    const progress = within(screen.getByLabelText('Current progress for Task running'));
    expect(progress.getByRole('progressbar')).toHaveAttribute('value', '3');
    expect(progress.getByRole('progressbar')).toHaveAttribute('max', '5');
    expect(progress.getByText('imported: 2')).toBeVisible();
    fireEvent.click(within(queued).getByRole('button'));
    expect(within(screen.getByLabelText('Current progress for Task queued')).queryByRole('progressbar')).not.toBeInTheDocument();
    fireEvent.click(within(table).getByRole('button', { name: 'Task failed' }));
    expect(within(screen.getByLabelText('Current progress for Task failed')).getByRole('alert')).toHaveTextContent('History was not fully verified');
    expect(within(screen.getByLabelText('Current progress for Task failed')).getByText('failed: 2')).toBeVisible();
  });

  it('filters by latest result, clears filters and refreshes without accepting work submissions', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([]);
    renderTasks();
    expect(await screen.findByText('No background tasks match this view.')).toBeVisible();
    fireEvent.change(screen.getByRole('combobox', { name: 'Task status' }), { target: { value: 'failed' } });
    expect(backgroundTasks).toHaveBeenLastCalledWith('', expect.any(AbortSignal), 'local');
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(screen.getByRole('combobox')).toHaveValue('');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled());
    const count = vi.mocked(backgroundTasks).mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(backgroundTasks).toHaveBeenCalledTimes(count + 1));
    expect(screen.queryByRole('button', { name: 'Start task' })).not.toBeInTheDocument();
  });

  it('keeps machine ownership in the details rather than repeating it in every row', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('same', { title: 'Local scan' })]);
    renderTasks();
    const table = await screen.findByRole('table');
    expect(within(table).queryByLabelText('This machine')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Task machine:/ })).not.toBeInTheDocument();
    fireEvent.click(within(table).getByRole('button', { name: 'Local scan' }));
    expect(screen.getByLabelText('This machine')).toBeVisible();
  });

  it('shows a skeleton and retryable read failure rather than a false empty result', async () => {
    vi.mocked(backgroundTasks).mockReturnValue(new Promise(() => {}));
    const first = renderTasks();
    expect(screen.getByRole('status', { name: 'Loading background tasks' }).children).toHaveLength(5);
    first.unmount();
    vi.mocked(backgroundTasks).mockRejectedValue(new Error('Database unavailable'));
    renderTasks();
    expect(await screen.findByRole('alert')).toHaveTextContent('Database unavailable');
    expect(screen.queryByText('No background tasks match this view.')).not.toBeInTheDocument();
    vi.mocked(backgroundTasks).mockResolvedValue([task('recovered')]);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Task recovered')).toBeVisible();
  });

  it('does not poll a hidden document and aborts reads on unmount', async () => {
    vi.mocked(useDocumentVisible).mockReturnValue(false);
    const first = renderTasks();
    expect(backgroundTasks).not.toHaveBeenCalled();
    first.unmount();
    vi.mocked(useDocumentVisible).mockReturnValue(true);
    vi.mocked(backgroundTasks).mockReturnValue(new Promise(() => {}));
    const second = renderTasks();
    await waitFor(() => expect(backgroundTasks).toHaveBeenCalledOnce());
    const signal = vi.mocked(backgroundTasks).mock.calls[0][1];
    second.unmount();
    expect(signal?.aborted).toBe(true);
  });

  it('explains the API limit while keeping the overview bounded', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue(Array.from({ length: 200 }, (_, i) => task(String(i))));
    renderTasks();
    expect(await screen.findByText('Tasks and history are limited to 200 available records. Older tasks and runs may not appear.')).toBeVisible();
    expect(within(screen.getByRole('table')).getAllByRole('button')).toHaveLength(200);
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('keeps cadence, lifetime run count, timezone and scope in the selected group', async () => {
    const nextRunAt = Date.now() + 3_600_000;
    vi.mocked(backgroundTasks).mockResolvedValue([task('cleanup', {
      title: 'Clean archived worktrees', status: 'succeeded', ownerId: 'workstation', scope: 'Project inventory',
      schedule: { intervalMillis: 3_600_000, nextRunAt, runs: 42 },
      progress: { phase: 'Checking checkout safety', completed: 5, total: 5, unit: 'checkouts', counters: { removed: 2, retained: 3 } },
    })]);
    renderTasks();
    const toggle = await screen.findByRole('button', { name: 'Clean archived worktrees' });
    expect(toggle.closest('tr')).toHaveTextContent('Succeeded');
    expect(screen.queryByText('42 runs')).not.toBeInTheDocument();
    fireEvent.click(toggle);
    const details = within(screen.getByRole('region', { name: 'Task group details for Clean archived worktrees' }));
    const schedule = within(details.getByLabelText('Schedule for Clean archived worktrees'));
    expect(schedule.getByText('Recurring · every 1h 0m')).toBeVisible();
    expect(details.getByText('Project inventory')).toBeVisible();
    expect(details.getByText('42 runs')).toBeVisible();
    expect(schedule.getByText(new RegExp(Intl.DateTimeFormat().resolvedOptions().timeZone))).toHaveAttribute('dateTime', new Date(nextRunAt).toISOString());
    const progress = within(details.getByLabelText('Current progress for Clean archived worktrees'));
    expect(progress.getByText('removed: 2')).toBeVisible();
    expect(progress.getByText('retained: 3')).toBeVisible();
    expect(details.getByText('Waiting for next run · Last run: Succeeded')).toBeVisible();
  });

  it('does not claim another run is scheduled after the loop stopped', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('once', { schedule: { intervalMillis: 60_000, runs: 1, nextRunAt: 0 } })]);
    renderTasks();
    fireEvent.click(await screen.findByRole('button', { name: 'Task once' }));
    expect(screen.getByText('Recurring · every 1m 0s')).toBeVisible();
    expect(screen.getByText('1 run')).toBeVisible();
    expect(screen.queryByText(/Next check:/)).not.toBeInTheDocument();
  });
});
