// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { renderWithQuery } from '../test/renderWithQuery';
import { backgroundTasks, type BackgroundTask } from '../lib/backgroundTasks';
import { BackgroundTasks } from './BackgroundTasks';

vi.mock('../lib/backgroundTasks', () => ({ backgroundTasks: vi.fn() }));

function task(id: string, overrides: Partial<BackgroundTask> = {}): BackgroundTask {
  return { id, key: 'scan', title: 'History scan', kind: 'history', ownerId: 'local', status: 'running',
    createdAt: 1000, updatedAt: 2000,
    progress: { phase: 'Checking sessions', completed: 3, total: 5, unit: 'sessions', counters: { imported: 2 } },
    ...overrides };
}

function Location() {
  const location = useLocation();
  const navigate = useNavigate();
  return <><output aria-label="URL">{location.search}</output><button onClick={() => void navigate(-1)}>Back</button></>;
}

function renderTasks(url = '/') {
  return renderWithQuery(<MemoryRouter initialEntries={[url]}><BackgroundTasks /><Location /></MemoryRouter>);
}

describe('Selected task group', () => {
  it('updates progress without moving rows or losing the selection when API order and run IDs change', async () => {
    const scan = task('one');
    const cleanup = task('cleanup', { kind: 'cleanup', key: 'cleanup', title: 'Cleanup' });
    vi.mocked(backgroundTasks).mockResolvedValue([scan, cleanup]);
    const { client } = renderTasks();
    const table = await screen.findByRole('table');
    const titles = () => within(table).getAllByRole('button').map(row => row.textContent);
    const before = titles();
    const toggle = within(table).getByRole('button', { name: 'History scan' });
    fireEvent.click(toggle);
    vi.mocked(backgroundTasks).mockResolvedValue([cleanup, task('two', { createdAt: 3000,
      progress: { phase: 'Importing', completed: 4, total: 5, unit: 'sessions', counters: {} } }), scan]);
    await client.invalidateQueries({ queryKey: ['background-tasks'] });
    expect(await within(screen.getByLabelText('Current progress for History scan')).findByText('4 / 5 sessions')).toBeVisible();
    expect(titles()).toEqual(before);
    expect(within(table).getByRole('button', { name: 'History scan' })).toBe(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(screen.getByRole('button', { name: 'Close task details' }));
    expect(toggle).toHaveFocus();
    expect(screen.queryByRole('region', { name: /Task group details/ })).not.toBeInTheDocument();
  });

  it('shows unknown totals only for the selected task, and no fake percentages', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('unknown', {
      progress: { phase: 'Discovering', completed: 7, total: null, unit: 'sessions', counters: {} } })]);
    renderTasks();
    fireEvent.click(await screen.findByRole('button', { name: 'History scan' }));
    expect(within(screen.getByLabelText('Current progress for History scan')).getByText('7 sessions · total unknown')).toBeVisible();
    expect(within(screen.getByLabelText('Current progress for History scan')).getByRole('progressbar')).not.toHaveAttribute('value');
  });

  it('does not let a newer finished run hide an older active execution', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('finished', { status: 'succeeded', createdAt: 5000 }), task('active')]);
    renderTasks();
    expect((await screen.findByRole('button', { name: 'History scan' })).closest('tr')).toHaveTextContent('Running');
  });

  it('shows unitless progress and finished unknown totals without implying ongoing work', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([
      task('unitless', { key: 'unitless', title: 'Unitless', progress: { phase: '', completed: 2, total: 4, counters: {} } }),
      task('finished', { key: 'finished', title: 'Finished', status: 'succeeded',
        progress: { phase: 'Done', completed: 3, total: null, counters: {} }, schedule: { intervalMillis: 60_000, runs: 3 } }),
      task('zero', { key: 'zero', title: 'Zero', status: 'succeeded', progress: { phase: '', completed: 0, total: 0, counters: {} } }),
    ]);
    renderTasks();
    fireEvent.click(await screen.findByRole('button', { name: 'Unitless' }));
    expect(within(screen.getByLabelText('Current progress for Unitless')).getByText('2 / 4')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Finished' }));
    expect(within(screen.getByLabelText('Current progress for Finished')).getByText('3 processed')).toBeVisible();
    expect(screen.queryByText(/total unknown/)).not.toBeInTheDocument();
    expect(screen.getByText('Available run history. Recurring jobs retain only their latest run.')).toBeVisible();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Zero' }));
    expect(screen.queryByText(/processed/)).not.toBeInTheDocument();
  });

  it('filters the latest state rather than resurrecting failed history and preserves URL state', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('old', { status: 'failed' }),
      task('new', { status: 'succeeded', createdAt: 3000 }), task('other', { key: 'other', title: 'Failed cleanup', status: 'failed' })]);
    renderTasks('/sessions?activity=tasks&taskStatus=failed&keep=yes');
    expect(await screen.findByText('Failed cleanup')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'History scan' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(screen.getByRole('button', { name: 'History scan' }).closest('tr')).toHaveTextContent('Succeeded');
    expect(screen.getByLabelText('URL')).toHaveTextContent('?activity=tasks&keep=yes');
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByRole('combobox')).toHaveValue('failed');
  });

  it('ignores invalid filters and retains the table and selection during retryable refresh failures', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('one')]);
    const { client } = renderTasks('/?taskStatus=invalid');
    fireEvent.click(await screen.findByRole('button', { name: 'History scan' }));
    expect(screen.getByRole('combobox')).toHaveValue('');
    vi.mocked(backgroundTasks).mockRejectedValue(new Error('Offline'));
    await client.invalidateQueries({ queryKey: ['background-tasks'] });
    expect(await screen.findByRole('alert')).toHaveTextContent('Offline');
    expect(screen.getByRole('table')).toBeVisible();
    expect(screen.getByRole('region', { name: 'Task group details for History scan' })).toBeVisible();
    expect(screen.queryByText('No background tasks match this view.')).not.toBeInTheDocument();
    vi.mocked(backgroundTasks).mockResolvedValue([task('one')]);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('table')).toBeVisible();
  });
});
