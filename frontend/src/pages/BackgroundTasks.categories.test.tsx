// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { renderWithQuery } from '../test/renderWithQuery';
import { backgroundTasks, type BackgroundTask } from '../lib/backgroundTasks';
import { BackgroundTasks } from './BackgroundTasks';

vi.mock('../lib/backgroundTasks', () => ({ backgroundTasks: vi.fn() }));
vi.mock('../lib/settingsQueries', () => ({ useRemotes: () => ({ data: [
  { remoteId: 'workstation', displayName: 'Workstation', enabled: true, health: 'connected' },
  { remoteId: 'offline', displayName: 'Offline machine', enabled: true, health: 'disconnected' },
] }) }));

function task(kind: string, title: string): BackgroundTask {
  return { id: kind, kind, title, ownerId: 'local', status: 'succeeded', createdAt: 1000, updatedAt: 2000,
    progress: { phase: 'Done', completed: 0, total: null, counters: {} } };
}

function Location() {
  const location = useLocation();
  const navigate = useNavigate();
  return <><output aria-label="URL">{location.search}</output><button onClick={() => void navigate(-1)}>Back</button></>;
}

it('shows named categories with all tasks alphabetically sorted and no pagination', async () => {
  vi.mocked(backgroundTasks).mockResolvedValue([
    task('schedule.worktree-cleanup', 'Clean archived worktrees'),
    task('schedule.auto-archive', 'Archive inactive sessions and projects'),
    task('schedule.snooze', 'Wake snoozed sessions'),
    task('schedule.web-push', 'Check notification candidates'),
    task('schedule.pr-watch', 'Check watched pull requests'),
    task('schedule.database-size', 'Sample database sizes'),
    task('conversation.history', 'Sync conversation history'), task('unknown', 'Other work'),
  ]);
  renderWithQuery(<MemoryRouter><BackgroundTasks /></MemoryRouter>);
  const table = await screen.findByRole('table', { name: 'Background task groups' });
  expect(within(table).getAllByRole('button').map(button => button.textContent)).toEqual([
    'Archive inactive sessions and projects', 'Clean archived worktrees', 'Wake snoozed sessions',
    'Check notification candidates', 'Check watched pull requests', 'Sample database sizes',
    'Sync conversation history', 'Other work',
  ]);
  for (const name of ['Housekeeping', 'Session and runtime coordination', 'Notifications and plugins',
    'Scheduled automation and synchronization', 'Metrics and accounting', 'History synchronization', 'Other tasks']) {
    expect(screen.getByRole('heading', { name: new RegExp(name) })).toBeVisible();
  }
  expect(screen.getByRole('region', { name: 'Task list' })).toHaveAttribute('tabindex', '0');
  expect(screen.queryByRole('button', { name: 'Next task groups' })).not.toBeInTheDocument();
  expect(screen.queryByLabelText('This machine')).not.toBeInTheDocument();
  expect(backgroundTasks).toHaveBeenLastCalledWith('', expect.any(AbortSignal), 'local');
});

it('reveals a machine chooser from the tab and scopes requests and URL history to its owner', async () => {
  vi.mocked(backgroundTasks).mockImplementation(async (_status, _signal, owner) => [
    { ...task('conversation.history', `${owner} sync`), ownerId: owner ?? 'local' },
  ]);
  renderWithQuery(<MemoryRouter initialEntries={['/?activity=tasks&keep=yes']}><BackgroundTasks /><Location /></MemoryRouter>);
  fireEvent.click(await screen.findByRole('button', { name: 'local sync' }));
  expect(screen.getByRole('region', { name: 'Task group details for local sync' })).toBeVisible();
  expect(screen.queryByRole('combobox', { name: 'Task machine' })).not.toBeInTheDocument();
  const tab = screen.getByRole('button', { name: 'Task machine: This machine' });
  fireEvent.click(tab);
  expect(tab).toHaveAttribute('aria-expanded', 'true');
  fireEvent.change(screen.getByRole('combobox', { name: 'Task machine' }), { target: { value: 'workstation' } });
  await screen.findByRole('button', { name: 'workstation sync' });
  expect(screen.queryByRole('region', { name: /Task group details/ })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'local sync' })).not.toBeInTheDocument();
  expect(backgroundTasks).toHaveBeenLastCalledWith('', expect.any(AbortSignal), 'workstation');
  expect(screen.getByLabelText('URL')).toHaveTextContent('activity=tasks&keep=yes&taskMachine=workstation');
  expect(screen.getByLabelText('URL')).not.toHaveTextContent('taskGroup');
  expect(screen.queryByRole('combobox', { name: 'Task machine' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Back' }));
  await screen.findByRole('button', { name: 'local sync' });
});

it('preserves an unavailable owner from the URL rather than falling back to local tasks', async () => {
  vi.mocked(backgroundTasks).mockRejectedValue(new Error('background task owner unavailable'));
  renderWithQuery(<MemoryRouter initialEntries={['/?taskMachine=missing']}><BackgroundTasks /></MemoryRouter>);
  expect(await screen.findByRole('alert')).toHaveTextContent('background task owner unavailable');
  expect(backgroundTasks).toHaveBeenLastCalledWith('', expect.any(AbortSignal), 'missing');
  fireEvent.click(screen.getByRole('button', { name: 'Task machine: missing' }));
  expect(screen.getByRole('option', { name: 'missing (offline)' })).toBeVisible();
});
