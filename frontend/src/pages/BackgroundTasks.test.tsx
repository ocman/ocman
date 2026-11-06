// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { renderWithQuery } from '../test/renderWithQuery';
import { backgroundTasks, type BackgroundTask } from '../lib/backgroundTasks';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { BackgroundTasks } from './BackgroundTasks';

vi.mock('../lib/backgroundTasks', () => ({ backgroundTasks: vi.fn() }));
vi.mock('../lib/usePanelVisible', () => ({ useDocumentVisible: vi.fn(() => true) }));

function task(id: string, overrides: Partial<BackgroundTask> = {}): BackgroundTask {
  return {
    id, title: `Task ${id}`, kind: 'conversation.history', ownerId: 'local', status: 'running',
    progress: { phase: 'checking sessions', completed: 3, total: 5, unit: 'sessions', counters: { imported: 2, unchanged: 1 } },
    createdAt: 1000, updatedAt: 2000, ...overrides,
  };
}

describe('Background tasks', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(useDocumentVisible).mockReturnValue(true);
  });

  it('shows active progress, unknown totals, and failed scans without implying hydration succeeded', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([
      task('running'),
      task('queued', { status: 'queued', progress: { phase: 'queued', completed: 0, total: null, counters: {} } }),
      task('failed', { status: 'failed', error: '2 sessions failed; history was not fully verified', progress: { phase: 'checking sessions', completed: 5, total: 5, unit: 'sessions', counters: { failed: 2 } } }),
      task('interrupted', { status: 'interrupted', error: 'Work was not replayed.' }),
      task('cancelled', { status: 'cancelled' }),
    ]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('Task running')).toBeVisible();
    expect(within(screen.getByRole('region', { name: 'Active tasks' })).getByText('Task queued')).toBeVisible();
    expect(within(screen.getByRole('region', { name: 'Recent tasks' })).getByText('Task failed')).toBeVisible();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /Task running/ }));
    expect(screen.getByRole('progressbar', { name: 'Progress for Task running' })).toHaveAttribute('value', '3');
    expect(screen.getByRole('progressbar', { name: 'Progress for Task running' })).toHaveAttribute('max', '5');
    expect(screen.getByText('imported: 2')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /Task queued/ }));
    expect(screen.getByRole('progressbar', { name: 'Progress for Task queued' })).not.toHaveAttribute('value');
    fireEvent.click(screen.getByRole('button', { name: /Task failed/ }));
    expect(screen.getByText('failed: 2')).toBeVisible();
    expect(screen.getAllByRole('alert').map((element) => element.textContent)).toContain('2 sessions failed; history was not fully verified');
    const recent = within(screen.getByRole('region', { name: 'Recent tasks' }));
    expect(recent.getByText('Cancelled')).toBeVisible();
    expect(recent.getByText('Interrupted')).toBeVisible();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Task failed/ })).toHaveFocus();
  });

  it('filters by status and refreshes without accepting work submissions', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('No background tasks match this view.')).toBeVisible();
    fireEvent.change(screen.getByRole('combobox', { name: 'Task status' }), { target: { value: 'failed' } });
    await waitFor(() => expect(backgroundTasks).toHaveBeenLastCalledWith('failed', expect.any(AbortSignal)));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled());
    const count = vi.mocked(backgroundTasks).mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(backgroundTasks).toHaveBeenCalledTimes(count + 1));
    expect(screen.queryByRole('button', { name: 'Start task' })).not.toBeInTheDocument();
  });

  it('distinguishes local and remote tasks even when their IDs collide', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([
      task('same', { title: 'Local scan' }),
      task('same', { title: 'Remote scan', ownerId: 'workstation' }),
    ]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('Remote scan')).toBeVisible();
    expect(screen.getByLabelText('This machine')).toBeVisible();
    expect(screen.getByLabelText('workstation')).toBeVisible();
  });

  it('shows loading and read failures rather than a false empty result', async () => {
    vi.mocked(backgroundTasks).mockReturnValue(new Promise(() => {}));
    const first = renderWithQuery(<BackgroundTasks />);
    expect(screen.getByRole('status')).toHaveTextContent('Loading background tasks');
    first.unmount();
    vi.mocked(backgroundTasks).mockRejectedValue(new Error('Database unavailable'));
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByRole('alert')).toHaveTextContent('Database unavailable');
    expect(screen.queryByText('No background tasks match this view.')).not.toBeInTheDocument();
  });

  it('does not start polling while the document is hidden and aborts reads on unmount', async () => {
    vi.mocked(useDocumentVisible).mockReturnValue(false);
    const first = renderWithQuery(<BackgroundTasks />);
    expect(backgroundTasks).not.toHaveBeenCalled();
    first.unmount();
    vi.mocked(useDocumentVisible).mockReturnValue(true);
    vi.mocked(backgroundTasks).mockReturnValue(new Promise(() => {}));
    const second = renderWithQuery(<BackgroundTasks />);
    await waitFor(() => expect(backgroundTasks).toHaveBeenCalledOnce());
    const signal = vi.mocked(backgroundTasks).mock.calls[0][1];
    second.unmount();
    expect(signal?.aborted).toBe(true);
  });

  it('explains the bounded list and supports tasks without a known unit or total', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue(Array.from({ length: 200 }, (_, i) => task(String(i), {
      status: 'succeeded', progress: { phase: 'finished', completed: 0, total: null, counters: {} },
    })));
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('Showing up to 200 matching tasks, active work first.')).toBeVisible();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('shows recurring cleanup progress, cadence, next check timezone and owner scope', async () => {
    const nextRunAt = Date.now() + 3_600_000;
    vi.mocked(backgroundTasks).mockResolvedValue([task('cleanup', {
      title: 'Clean archived worktrees', status: 'succeeded', ownerId: 'workstation', scope: 'Project inventory',
      schedule: { intervalMillis: 3_600_000, nextRunAt, runs: 2 },
      progress: { phase: 'Checking checkout safety', completed: 5, total: 5, unit: 'checkouts', counters: { removed: 2, retained: 3 } },
    })]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('Clean archived worktrees')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /Clean archived worktrees/ }));
    const schedule = within(screen.getByLabelText('Schedule for Clean archived worktrees'));
    expect(schedule.getByText('Recurring · every 1h 0m · 2 runs')).toBeVisible();
    expect(schedule.getByText('Scope: Project inventory')).toBeVisible();
    expect(schedule.getByText(new RegExp(Intl.DateTimeFormat().resolvedOptions().timeZone))).toHaveAttribute('dateTime', new Date(nextRunAt).toISOString());
    expect(screen.getByText('removed: 2')).toBeVisible();
    expect(screen.getByText('retained: 3')).toBeVisible();
    expect(screen.getByLabelText('workstation')).toBeVisible();
  });

  it('does not claim another run is scheduled after its loop stopped', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('once', { schedule: { intervalMillis: 60_000, runs: 1, nextRunAt: 0 } })]);
    renderWithQuery(<BackgroundTasks />);
    fireEvent.click(await screen.findByRole('button', { name: /Task once/ }));
    expect(await screen.findByText('Recurring · every 1m 0s · 1 run')).toBeVisible();
    expect(screen.queryByText(/Next check:/)).not.toBeInTheDocument();
  });
});
