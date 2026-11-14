// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import type { ReactElement } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { renderWithQuery as renderQuery } from '../test/renderWithQuery';
import { backgroundTasks, type BackgroundTask } from '../lib/backgroundTasks';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { BackgroundTasks } from './BackgroundTasks';

vi.mock('../lib/backgroundTasks', () => ({ backgroundTasks: vi.fn() }));
vi.mock('../lib/usePanelVisible', () => ({ useDocumentVisible: vi.fn(() => true) }));

function renderWithQuery(ui: ReactElement) {
  return renderQuery(<MemoryRouter>{ui}</MemoryRouter>);
}

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

  it('groups runs by task identity and expands newest-first history inline', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([
      task('old', { key: 'scan', title: 'History scan', status: 'failed', createdAt: 1000, error: 'Previous scan failed' }),
      task('new', { key: 'scan', title: 'History scan', createdAt: 3000 }),
      task('remote', { key: 'scan', title: 'History scan', ownerId: 'remote' }),
      task('scoped', { key: 'scan', title: 'History scan', scope: 'Other project' }),
      task('kind', { key: 'scan', title: 'History scan', kind: 'other.kind' }),
      task('other-key', { key: 'other', title: 'History scan' }),
    ]);
    renderWithQuery(<BackgroundTasks />);
    const list = await screen.findByRole('list', { name: 'Background tasks' });
    const rows = within(list).getAllByRole('listitem');
    expect(rows).toHaveLength(5);
    const row = rows.find(row => within(row).getByRole('button').textContent?.includes('2 runs'))!;
    const toggle = within(row).getByRole('button');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(toggle).toHaveTextContent('Running');
    expect(screen.queryByText('Previous scan failed')).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    const history = within(row).getByRole('region', { name: 'History for History scan' });
    expect(history).toHaveAttribute('id', toggle.getAttribute('aria-controls'));
    const runs = within(history).getAllByRole('listitem');
    expect(runs).toHaveLength(2);
    expect(runs[0]).toHaveTextContent('Running');
    expect(runs[1]).toHaveTextContent('Previous scan failed');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(screen.queryByRole('region', { name: 'History for History scan' })).not.toBeInTheDocument();
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
    const list = screen.getByRole('list', { name: 'Background tasks' });
    expect(screen.getAllByRole('list')).toHaveLength(1);
    expect(within(list).getAllByRole('listitem')).toHaveLength(5);
    expect(screen.queryByRole('region', { name: 'Active tasks' })).not.toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Recent tasks' })).not.toBeInTheDocument();
    const running = screen.getByRole('button', { name: /Task running/ });
    expect(running.querySelector('.oc-loading-spinner')).toHaveAttribute('aria-hidden', 'true');
    expect(screen.getByRole('button', { name: /Task queued/ }).querySelector('.oc-loading-spinner')).toBeNull();
    expect(screen.getByRole('button', { name: /Task failed/ }).querySelector('.oc-loading-spinner')).toBeNull();
    expect(within(screen.getByLabelText('Current progress for Task running')).getByRole('progressbar')).toHaveAttribute('value', '3');
    fireEvent.click(screen.getByRole('button', { name: /Task running/ }));
    const details = within(screen.getByRole('region', { name: 'History for Task running' }));
    expect(details.getByRole('progressbar', { name: 'Progress for Task running' })).toHaveAttribute('value', '3');
    expect(details.getByRole('progressbar', { name: 'Progress for Task running' })).toHaveAttribute('max', '5');
    expect(details.getByText('imported: 2')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /Task queued/ }));
    expect(screen.queryByRole('progressbar', { name: 'Progress for Task queued' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /Task failed/ }));
    expect(screen.getAllByText('failed: 2').length).toBeGreaterThan(0);
    expect(screen.getAllByRole('alert').map((element) => element.textContent)).toContain('2 sessions failed; history was not fully verified');
    expect(within(list).getByText('Cancelled')).toBeVisible();
    expect(within(list).getByText('Interrupted')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /Task failed/ }));
    expect(screen.queryByRole('region', { name: 'History for Task failed' })).not.toBeInTheDocument();
  });

  it('filters by status and refreshes without accepting work submissions', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('No background tasks match this view.')).toBeVisible();
    fireEvent.change(screen.getByRole('combobox', { name: 'Task status' }), { target: { value: 'failed' } });
    expect(backgroundTasks).toHaveBeenLastCalledWith('', expect.any(AbortSignal));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled());
    const count = vi.mocked(backgroundTasks).mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(backgroundTasks).toHaveBeenCalledTimes(count + 1));
    expect(screen.queryByRole('button', { name: 'Start task' })).not.toBeInTheDocument();
  });

  it('uses stable identity ordering and indeterminate bars for active unknown totals', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([
      task('finished', { status: 'succeeded' }),
      task('unknown', { progress: { phase: 'working', completed: 2, total: null, counters: {} } }),
      task('zero', { progress: { phase: 'discovering', completed: 0, total: 0, counters: {} } }),
      task('waiting', { status: 'queued' }),
    ]);
    renderWithQuery(<BackgroundTasks />);
    const list = await screen.findByRole('list', { name: 'Background tasks' });
    const rows = within(list).getAllByRole('listitem');
    expect(rows.map((row) => within(row).getByRole('button').textContent)).toEqual([
      expect.stringContaining('Task finished'), expect.stringContaining('Task unknown'),
      expect.stringContaining('Task waiting'), expect.stringContaining('Task zero'),
    ]);
    expect(within(list).getAllByRole('progressbar')).toHaveLength(4);
    expect(within(rows[0]).getByRole('progressbar')).toHaveAttribute('max', '5');
    expect(rows[1].querySelector('.oc-loading-spinner')).toBeInTheDocument();
    expect(rows[3].querySelector('.oc-loading-spinner')).toBeInTheDocument();
    fireEvent.click(within(rows[1]).getByRole('button'));
    expect(within(screen.getByRole('region', { name: 'History for Task unknown' })).getByRole('progressbar')).not.toHaveAttribute('value');
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
    expect(await screen.findByText('Tasks and history are limited to 200 available records. Older tasks and runs may not appear.')).toBeVisible();
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
    const schedule = within(screen.getByLabelText('Schedule for Clean archived worktrees'));
    expect(schedule.getByText('Recurring · every 1h 0m')).toBeVisible();
    expect(screen.getByText('Project inventory')).toBeVisible();
    expect(screen.getByText('2 runs')).toBeVisible();
    expect(schedule.getByText(new RegExp(Intl.DateTimeFormat().resolvedOptions().timeZone))).toHaveAttribute('dateTime', new Date(nextRunAt).toISOString());
    expect(screen.getByText('removed: 2')).toBeVisible();
    expect(screen.getByText('retained: 3')).toBeVisible();
    expect(screen.getByLabelText('workstation')).toBeVisible();
  });

  it('does not claim another run is scheduled after its loop stopped', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('once', { schedule: { intervalMillis: 60_000, runs: 1, nextRunAt: 0 } })]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('Recurring · every 1m 0s')).toBeVisible();
    expect(screen.getByText('1 run')).toBeVisible();
    expect(screen.queryByText(/Next check:/)).not.toBeInTheDocument();
  });
});
