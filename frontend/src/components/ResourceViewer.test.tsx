// @vitest-environment jsdom
import { act, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { renderWithQuery } from '../test/renderWithQuery';
import { resources, type ResourceSnapshot } from '../lib/resources';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { ResourceViewer } from './ResourceViewer';
import { MemoryRouter } from 'react-router-dom';

function renderViewer() {
  return renderWithQuery(<MemoryRouter><ResourceViewer /></MemoryRouter>);
}

vi.mock('../lib/resources', () => ({ resources: vi.fn() }));
vi.mock('../lib/usePanelVisible', () => ({ useDocumentVisible: vi.fn(() => true) }));

const snapshot: ResourceSnapshot = {
  timestamp: 10000, cpuPercent: 150, memoryBytes: 2 * 1024 ** 3,
  processes: [
    { pid: 1, parentPid: 0, name: 'ocman', cpuPercent: 25, memoryBytes: 1024 ** 3 },
    { pid: 2, parentPid: 1, name: 'agent', cpuPercent: 125, memoryBytes: 10 * 1024 ** 2 },
  ],
  history: [
    { timestamp: 9000, cpuPercent: 100, memoryBytes: 1024 ** 3 },
    { timestamp: 10000, cpuPercent: 150, memoryBytes: 2 * 1024 ** 3 },
  ],
};
snapshot.history[0].processes = snapshot.processes.map((process) => ({ ...process, cpuPercent: process.cpuPercent * 2 / 3 }));
snapshot.history[1].processes = snapshot.processes;

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useDocumentVisible).mockReturnValue(true);
  vi.mocked(resources).mockResolvedValue(snapshot);
});
afterEach(() => vi.useRealTimers());

it('shows CPU and memory graphs and sorts the top-like process list', async () => {
  renderViewer();
  const table = await screen.findByRole('table', { name: 'Ocman processes' });
  expect(screen.getByRole('group', { name: 'CPU history' })).toBeVisible();
  expect(screen.getByRole('group', { name: 'Memory history' })).toBeVisible();
  expect(screen.getByText('150.0%')).toBeVisible();
  expect(screen.getByText('2.00 GiB')).toBeVisible();
  const names = () => within(table).getAllByRole('row').slice(1).map((row) => within(row).getAllByRole('cell')[2].textContent);
  expect(names()).toEqual(['agent', 'ocman']);
  fireEvent.change(screen.getByLabelText('Sort processes'), { target: { value: 'memoryBytes' } });
  expect(names()).toEqual(['ocman', 'agent']);
  fireEvent.change(screen.getByLabelText('Sort processes'), { target: { value: 'pid' } });
  expect(names()).toEqual(['ocman', 'agent']);
  fireEvent.change(screen.getByLabelText('Sort processes'), { target: { value: 'name' } });
  expect(names()).toEqual(['agent', 'ocman']);
});

it('shows full commands and links known sessions without selecting the process', async () => {
  vi.mocked(resources).mockResolvedValue({ ...snapshot, processes: [
    { ...snapshot.processes[0], command: '/usr/bin/ocman serve --port 8222', sessionId: 'session/one' },
    snapshot.processes[1],
  ] });
  renderViewer();
  expect(await screen.findByText('/usr/bin/ocman serve --port 8222')).toBeVisible();
  const link = screen.getByRole('link', { name: 'Open session' });
  expect(link).toHaveAttribute('href', '/session/session%2Fone');
  fireEvent.click(link);
  expect(screen.getByRole('button', { name: 'Select ocman, PID 1' })).toHaveAttribute('aria-pressed', 'false');
  expect(screen.getAllByRole('link')).toHaveLength(1);
});

it('isolates a process from its area or row, pins it through sorting and clears selection', async () => {
  renderViewer();
  await screen.findByText('ocman');
  fireEvent.click(screen.getByRole('button', { name: 'CPU: ocman, PID 1' }));
  expect(screen.getByRole('button', { name: 'Select ocman, PID 1' })).toHaveAttribute('aria-pressed', 'true');
  expect(screen.queryByRole('button', { name: 'CPU: agent, PID 2' })).not.toBeInTheDocument();
  const rows = () => within(screen.getByRole('table')).getAllByRole('row').slice(1);
  expect(rows()[0]).toHaveTextContent('ocman');
  fireEvent.change(screen.getByLabelText('Sort processes'), { target: { value: 'name' } });
  expect(rows()[0]).toHaveTextContent('ocman');
  fireEvent.click(screen.getByRole('button', { name: 'Show all processes' }));
  expect(screen.getByRole('button', { name: 'CPU: agent, PID 2' })).toBeVisible();
  fireEvent.click(rows()[0]);
  expect(screen.getByRole('button', { name: 'Select agent, PID 2' })).toHaveAttribute('aria-pressed', 'true');
});

it('shows loading, failure and retry without claiming there are no processes', async () => {
  vi.mocked(resources).mockReturnValueOnce(new Promise(() => {}));
  const pending = renderViewer();
  expect(screen.getByRole('status')).toHaveTextContent('Loading resources');
  pending.unmount();
  vi.mocked(resources).mockRejectedValueOnce(new Error('unavailable'));
  renderViewer();
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load process resources');
  expect(screen.queryByText('No managed processes are running.')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(await screen.findByText('ocman')).toBeVisible();
});

it('keeps selection pinned through polling and does not select a replacement with the same PID', async () => {
  const { client } = renderViewer();
  fireEvent.click(await screen.findByRole('button', { name: 'Select ocman, PID 1' }));
  vi.mocked(resources).mockResolvedValue({ ...snapshot, processes: snapshot.processes.map((process) => ({ ...process, cpuPercent: process.pid === 2 ? 999 : 0 })) });
  await act(() => client.refetchQueries({ queryKey: ['resources', 'local'] }));
  expect(within(screen.getByRole('table')).getAllByRole('row')[1]).toHaveTextContent('ocman');
  vi.mocked(resources).mockResolvedValue({ ...snapshot, history: [], processes: [{ ...snapshot.processes[0], name: 'replacement' }] });
  await act(() => client.refetchQueries({ queryKey: ['resources', 'local'] }));
  expect(screen.getByText('Selected process is no longer available')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Select replacement, PID 1' })).toHaveAttribute('aria-pressed', 'false');
  fireEvent.click(screen.getByRole('button', { name: 'Show all processes' }));
  expect(screen.queryByRole('button', { name: 'CPU: replacement, PID 1' })).not.toBeInTheDocument();
});

it('retains the last successful data when a refresh fails', async () => {
  const { client } = renderViewer();
  await screen.findByText('ocman');
  vi.mocked(resources).mockRejectedValueOnce(new Error('unavailable'));
  await act(() => client.refetchQueries({ queryKey: ['resources', 'local'] }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Showing the last sample');
  expect(screen.getByText('ocman')).toBeVisible();
});

it('shows successful empty results and graphs with no history', async () => {
  vi.mocked(resources).mockResolvedValue({ ...snapshot, processes: [], history: [] });
  renderViewer();
  expect(await screen.findByText('No managed processes are running.')).toBeVisible();
  expect(screen.getAllByText('No data')).toHaveLength(2);
});

it('polls once per second, pauses when hidden and aborts when unmounted', async () => {
  vi.mocked(useDocumentVisible).mockReturnValue(false);
  const hidden = renderViewer();
  expect(resources).not.toHaveBeenCalled();
  hidden.unmount();
  vi.mocked(useDocumentVisible).mockReturnValue(true);
  vi.useFakeTimers();
  const page = renderViewer();
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(resources).toHaveBeenCalledTimes(1);
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(resources).toHaveBeenCalledTimes(2);
  vi.mocked(resources).mockReturnValue(new Promise(() => {}));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  const signal = vi.mocked(resources).mock.calls.at(-1)![0];
  page.unmount();
  expect(signal.aborted).toBe(true);
  await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
  expect(resources).toHaveBeenCalledTimes(3);
});
