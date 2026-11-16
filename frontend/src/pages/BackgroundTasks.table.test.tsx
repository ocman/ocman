// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { renderWithQuery } from '../test/renderWithQuery';
import { backgroundTasks, type BackgroundTask } from '../lib/backgroundTasks';
import { BackgroundTasks } from './BackgroundTasks';

vi.mock('../lib/backgroundTasks', () => ({ backgroundTasks: vi.fn() }));

it('keeps the overview a five-row table and opens progress only for the selected group', async () => {
  vi.mocked(backgroundTasks).mockResolvedValue(Array.from({ length: 8 }, (_, index): BackgroundTask => ({
    id: String(index), key: `group-${index}`, title: `Task group ${index}`, kind: 'scan', ownerId: 'local',
    status: 'running', createdAt: 1000, updatedAt: 2000,
    progress: { phase: `Working on ${index}`, completed: 3, total: 5, counters: {} },
  })));
  renderWithQuery(<MemoryRouter><BackgroundTasks /></MemoryRouter>);
  const table = await screen.findByRole('table', { name: 'Background task groups' });
  expect(within(table).getAllByRole('row')).toHaveLength(6);
  expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  expect(screen.queryByRole('region', { name: /Task group details/ })).not.toBeInTheDocument();
  fireEvent.click(within(table).getByRole('button', { name: 'Task group 0' }));
  const details = screen.getByRole('region', { name: 'Task group details for Task group 0' });
  expect(within(details).getByLabelText('Current progress for Task group 0')).toHaveTextContent('Working on 0');
  expect(within(table).queryByText('Working on 0')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Next task groups' }));
  expect(within(table).getAllByRole('row')).toHaveLength(4);
  expect(screen.queryByRole('region', { name: /Task group details/ })).not.toBeInTheDocument();
});

function Location() {
  const location = useLocation();
  const navigate = useNavigate();
  return <><output aria-label="URL">{location.search}</output><button onClick={() => void navigate(-1)}>Back</button></>;
}

function task(index: number): BackgroundTask {
  return { id: String(index), key: `group-${index}`, title: `Task group ${index}`, kind: 'scan', ownerId: 'local',
    status: 'succeeded', createdAt: index * 1000, startedAt: index * 1000 + 100, updatedAt: index * 1000 + 200,
    progress: { phase: 'Done', completed: 0, total: null, counters: {} } };
}

it('restores pages and selected groups from the URL, preserves other parameters, and supports Back', async () => {
  const group = JSON.stringify(['local', 'scan', 'group-6', '']);
  vi.mocked(backgroundTasks).mockResolvedValue(Array.from({ length: 8 }, (_, index) => task(index)));
  renderWithQuery(<MemoryRouter initialEntries={[`/?activity=tasks&keep=yes&taskPage=2&taskGroup=${encodeURIComponent(group)}`]}>
    <BackgroundTasks /><Location />
  </MemoryRouter>);
  expect(await screen.findByRole('region', { name: 'Task group details for Task group 6' })).toBeVisible();
  expect(screen.getByText('Page 2 of 2')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Next task groups' })).toBeDisabled();
  const toggle = screen.getByRole('button', { name: 'Task group 6' });
  expect(toggle.closest('tr')!.querySelector('time')).toHaveAttribute('dateTime', new Date(6100).toISOString());
  fireEvent.click(screen.getByRole('button', { name: 'Previous task groups' }));
  expect(screen.getByText('Page 1 of 2')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Previous task groups' })).toBeDisabled();
  expect(screen.getByLabelText('URL')).toHaveTextContent('activity=tasks&keep=yes&taskPage=1');
  expect(screen.queryByRole('region', { name: /Task group details/ })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Back' }));
  expect(screen.getByRole('region', { name: 'Task group details for Task group 6' })).toBeVisible();
});

it.each(['-1', 'invalid', '1.5', '99999999999999999999', '100'])(
  'handles an invalid or out-of-range page %s without hiding available groups', async value => {
    vi.mocked(backgroundTasks).mockResolvedValue([task(1)]);
    renderWithQuery(<MemoryRouter initialEntries={[`/?taskPage=${value}`]}><BackgroundTasks /></MemoryRouter>);
    expect(await screen.findByRole('button', { name: 'Task group 1' })).toBeVisible();
  },
);

it('keeps a missing selection explicit instead of silently showing a different task', async () => {
  vi.mocked(backgroundTasks).mockResolvedValue([task(1)]);
  renderWithQuery(<MemoryRouter initialEntries={['/?taskGroup=missing']}><BackgroundTasks /></MemoryRouter>);
  expect(await screen.findByText('Task group no longer available')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Close task details' }));
  expect(screen.queryByText('Task group no longer available')).not.toBeInTheDocument();
});

it('selects whole rows but does not open a row while copying text or clicking a timestamp', async () => {
  vi.mocked(backgroundTasks).mockResolvedValue([task(1)]);
  renderWithQuery(<MemoryRouter><BackgroundTasks /></MemoryRouter>);
  const toggle = await screen.findByRole('button', { name: 'Task group 1' });
  const row = toggle.closest('tr')!;
  const selection = vi.spyOn(window, 'getSelection').mockReturnValue({ toString: () => 'selected text' } as Selection);
  fireEvent.click(within(row).getByText('Succeeded'));
  expect(toggle).toHaveAttribute('aria-expanded', 'false');
  selection.mockRestore();
  fireEvent.click(row.querySelector('time')!);
  expect(toggle).toHaveAttribute('aria-expanded', 'false');
  fireEvent.click(within(row).getByText('Succeeded'));
  expect(toggle).toHaveAttribute('aria-expanded', 'true');
});

it('caps recent history to five runs without losing the full run count', async () => {
  vi.mocked(backgroundTasks).mockResolvedValue(Array.from({ length: 8 }, (_, index) => ({ ...task(1), id: String(index), createdAt: index * 1000 })));
  renderWithQuery(<MemoryRouter><BackgroundTasks /></MemoryRouter>);
  fireEvent.click(await screen.findByRole('button', { name: 'Task group 1' }));
  expect(screen.getByText('8 runs')).toBeVisible();
  expect(screen.getByText('Showing the latest 5 of 8 available runs.')).toBeVisible();
  expect(within(screen.getByRole('region', { name: 'History for Task group 1' })).getAllByRole('listitem')).toHaveLength(5);
});
