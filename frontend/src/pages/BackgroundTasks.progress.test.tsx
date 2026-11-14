// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
import type { ReactElement } from 'react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { renderWithQuery as renderQuery } from '../test/renderWithQuery';
import { backgroundTasks, type BackgroundTask } from '../lib/backgroundTasks';
import { BackgroundTasks } from './BackgroundTasks';

vi.mock('../lib/backgroundTasks', () => ({ backgroundTasks: vi.fn() }));

function renderWithQuery(ui: ReactElement) {
  return renderQuery(<MemoryRouter>{ui}</MemoryRouter>);
}

function task(id: string, overrides: Partial<BackgroundTask> = {}): BackgroundTask {
  return { id, key: 'scan', title: 'History scan', kind: 'history', ownerId: 'local', status: 'running',
    createdAt: 1000, updatedAt: 2000,
    progress: { phase: 'Checking sessions', completed: 3, total: 5, unit: 'sessions', counters: { imported: 2 } },
    ...overrides };
}

describe('Task progress overview', () => {
  it('shows counts, counters and errors without expanding run history', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('failed', { status: 'failed', error: 'Import failed' })]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('3 / 5 sessions')).toBeVisible();
    expect(screen.getByText('imported: 2')).toBeVisible();
    expect(screen.getByRole('alert')).toHaveTextContent('Import failed');
    expect(screen.getByRole('button', { name: /History scan/ })).toHaveAttribute('aria-expanded', 'false');
  });

  it('updates progress in place and keeps history expanded when API order and runs change', async () => {
    const scan = task('one');
    const cleanup = task('cleanup', { kind: 'cleanup', key: 'cleanup', title: 'Cleanup' });
    vi.mocked(backgroundTasks).mockResolvedValue([scan, cleanup]);
    const { client } = renderWithQuery(<BackgroundTasks />);
    const list = await screen.findByRole('list', { name: 'Background tasks' });
    const titles = () => Array.from(list.children).map(row => within(row as HTMLElement).getByRole('button').textContent);
    const before = titles();
    const toggle = screen.getByRole('button', { name: /History scan/ });
    fireEvent.click(toggle);
    vi.mocked(backgroundTasks).mockResolvedValue([cleanup, task('two', { createdAt: 3000,
      progress: { phase: 'Importing', completed: 4, total: 5, unit: 'sessions', counters: {} } }), scan]);
    await client.invalidateQueries({ queryKey: ['background-tasks'] });
    expect(await within(screen.getByLabelText('Current progress for History scan')).findByText('4 / 5 sessions')).toBeVisible();
    expect(titles().map(title => title?.startsWith('Cleanup'))).toEqual(before.map(title => title?.startsWith('Cleanup')));
    expect(screen.getByRole('button', { name: /History scan/ })).toBe(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
  });

  it('shows scheduled waiting state and lifetime run count in the overview', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('schedule', { status: 'succeeded',
      schedule: { intervalMillis: 60_000, nextRunAt: Date.now() + 60_000, runs: 42 } })]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('42 runs')).toBeVisible();
    expect(screen.getByRole('button', { name: /History scan/ })).toHaveTextContent('Waiting for next run');
    expect(screen.getByText('Last run: Succeeded')).toBeVisible();
    expect(screen.getByText(/Next check:/)).toBeVisible();
  });

  it('shows indeterminate progress instead of hiding running work with no known total', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('unknown', {
      progress: { phase: 'Discovering', completed: 7, total: null, unit: 'sessions', counters: {} } })]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('7 sessions · total unknown')).toBeVisible();
    expect(screen.getByRole('progressbar')).not.toHaveAttribute('value');
  });

  it('does not let a newer finished run hide an older active execution', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('finished', { status: 'succeeded', createdAt: 5000 }), task('active')]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByRole('button', { name: /History scan/ })).toHaveTextContent('Running');
  });

  it('shows unitless counts and finished unknown totals without implying ongoing work', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([
      task('unitless', { key: 'unitless', title: 'Unitless',
        progress: { phase: '', completed: 2, total: 4, counters: {} } }),
      task('finished', { key: 'finished', title: 'Finished', status: 'succeeded',
        progress: { phase: 'Done', completed: 3, total: null, counters: {} },
        schedule: { intervalMillis: 60_000, runs: 3 } }),
    ]);
    renderWithQuery(<BackgroundTasks />);
    expect(await screen.findByText('2 / 4')).toBeVisible();
    expect(screen.getByText('3 processed')).toBeVisible();
    expect(screen.queryByText(/total unknown/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /Finished/ }));
    expect(screen.getByText('Available run history. Recurring jobs retain only their latest run.')).toBeVisible();
    expect(within(screen.getByRole('region', { name: 'History for Finished' })).queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('filters current task state rather than resurrecting failed history and preserves URL state', async () => {
    function Location() {
      const location = useLocation();
      const navigate = useNavigate();
      return <><output aria-label="URL">{location.search}</output><button onClick={() => void navigate(-1)}>Back</button></>;
    }
    vi.mocked(backgroundTasks).mockResolvedValue([task('old', { status: 'failed' }),
      task('new', { status: 'succeeded', createdAt: 3000 }), task('other', { key: 'other', title: 'Failed cleanup', status: 'failed' })]);
    renderQuery(<MemoryRouter initialEntries={['/sessions?activity=tasks&taskStatus=failed&keep=yes']}>
      <BackgroundTasks /><Location />
    </MemoryRouter>);
    expect(await screen.findByText('Failed cleanup')).toBeVisible();
    expect(screen.queryByRole('button', { name: /History scan/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(screen.getByRole('button', { name: /History scan/ })).toHaveTextContent('Succeeded');
    expect(screen.getByLabelText('URL')).toHaveTextContent('?activity=tasks&keep=yes');
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByRole('combobox')).toHaveValue('failed');
    expect(screen.queryByRole('button', { name: /History scan/ })).not.toBeInTheDocument();
  });

  it('ignores invalid URL filters and keeps the last successful tasks during refresh failure', async () => {
    vi.mocked(backgroundTasks).mockResolvedValue([task('one')]);
    const { client } = renderQuery(<MemoryRouter initialEntries={['/?taskStatus=invalid']}><BackgroundTasks /></MemoryRouter>);
    expect(await screen.findByText('History scan')).toBeVisible();
    expect(screen.getByRole('combobox')).toHaveValue('');
    vi.mocked(backgroundTasks).mockRejectedValue(new Error('Offline'));
    await client.invalidateQueries({ queryKey: ['background-tasks'] });
    expect(await screen.findByRole('alert')).toHaveTextContent('Offline');
    expect(screen.getByRole('button', { name: /History scan/ })).toBeVisible();
    expect(screen.queryByText('No background tasks match this view.')).not.toBeInTheDocument();
  });
});
