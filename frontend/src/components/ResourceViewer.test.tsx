// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { renderWithQuery } from '../test/renderWithQuery';
import { resources, type ResourceSnapshot } from '../lib/resources';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { useRemotes } from '../lib/settingsQueries';
import type { RemoteStatus } from '../lib/api.types';
import { ResourceViewer } from './ResourceViewer';
import { api } from '../lib/api';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';

function Location() {
  const navigate = useNavigate();
  return <><div aria-label="Location">{useLocation().search}</div><button onClick={() => navigate(-1)}>Back</button></>;
}

function renderViewer(search = '/') {
  return renderWithQuery(<MemoryRouter initialEntries={[search]}><ResourceViewer /><Location /></MemoryRouter>);
}

vi.mock('../lib/resources', () => ({ resources: vi.fn() }));
vi.mock('../lib/api', () => ({ api: { systemStats: vi.fn() } }));
vi.mock('../lib/usePanelVisible', () => ({ useDocumentVisible: vi.fn(() => true) }));
vi.mock('../lib/settingsQueries', () => ({ useRemotes: vi.fn() }));

function mockRemotes(remotes: Partial<RemoteStatus>[]) {
  vi.mocked(useRemotes).mockReturnValue({ data: remotes.map((remote, index) => ({
    localId: index + 1, displayName: '', address: '', enabled: true, health: 'connected', hostname: '',
    protocolVersion: 1, lastSeen: 0, sessionCount: 0, ...remote,
  })) } as ReturnType<typeof useRemotes>);
}

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
  vi.mocked(api.systemStats).mockResolvedValue({ uptime: 3661 } as Awaited<ReturnType<typeof api.systemStats>>);
  mockRemotes([]);
});
afterEach(() => vi.useRealTimers());

it('shows backend uptime independently of process resource loading', async () => {
  vi.mocked(resources).mockRejectedValue(new Error('unavailable'));
  renderViewer();
  expect(await screen.findByText('Backend uptime: 1h 1m')).toBeVisible();
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load process resources.');
});

it('keeps process resources available when uptime fails', async () => {
  vi.mocked(api.systemStats).mockRejectedValue(new Error('unavailable'));
  renderViewer();
  expect(await screen.findByText('Backend uptime unavailable')).toBeVisible();
  expect(await screen.findByRole('table', { name: 'Ocman processes' })).toBeVisible();
});

it('keeps the last uptime on refresh failure and supports retry', async () => {
  const { client } = renderViewer();
  await screen.findByText('Backend uptime: 1h 1m');
  vi.mocked(api.systemStats).mockRejectedValueOnce(new Error('unavailable'));
  await act(() => client.refetchQueries({ queryKey: ['resource-uptime'] }));
  expect(await screen.findByText('Uptime refresh failed. Showing the last sample.')).toBeVisible();
  expect(screen.getByText('Backend uptime: 1h 1m')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Retry uptime' }));
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Retry uptime' })).not.toBeInTheDocument());
});

it('loads uptime only while visible, refreshes every five seconds and aborts on unmount', async () => {
  vi.mocked(useDocumentVisible).mockReturnValue(false);
  const hidden = renderViewer();
  expect(api.systemStats).not.toHaveBeenCalled();
  hidden.unmount();
  vi.mocked(useDocumentVisible).mockReturnValue(true);
  vi.useFakeTimers();
  vi.mocked(api.systemStats).mockReturnValue(new Promise(() => {}));
  const page = renderViewer();
  expect(screen.getByText('Loading backend uptime…')).toBeVisible();
  const signal = vi.mocked(api.systemStats).mock.calls.at(-1)![0];
  page.unmount();
  expect(signal?.aborted).toBe(true);
  vi.mocked(api.systemStats).mockResolvedValue({ uptime: 61 } as Awaited<ReturnType<typeof api.systemStats>>);
  const polling = renderViewer();
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(api.systemStats).toHaveBeenCalledTimes(2);
  await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
  expect(api.systemStats).toHaveBeenCalledTimes(3);
  polling.unmount();
});

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

it('restores tree view from the URL, sorts siblings and preserves ancestry when selecting', async () => {
  vi.mocked(resources).mockResolvedValue({ ...snapshot, processes: [
    ...snapshot.processes,
    { pid: 3, parentPid: 1, name: 'worker', cpuPercent: 50, memoryBytes: 0 },
  ] });
  renderViewer('/?activity=resources&resourceView=tree&remoteId=box');
  const table = await screen.findByRole('table');
  const rows = () => within(table).getAllByRole('row').slice(1);
  expect(screen.getByRole('radio', { name: 'Tree' })).toBeChecked();
  expect(rows().map((row) => row.getAttribute('data-depth'))).toEqual(['0', '1', '1']);
  expect(rows()[0]).toHaveTextContent('ocman');
  expect(rows()[1]).toHaveTextContent('agent');
  fireEvent.click(screen.getByRole('button', { name: 'Select worker, PID 3' }));
  expect(rows()[0]).toHaveTextContent('ocman');
  expect(rows()[1]).toHaveTextContent('worker');
  fireEvent.change(screen.getByLabelText('Sort processes'), { target: { value: 'pid' } });
  fireEvent.click(screen.getByRole('button', { name: 'Show all processes' }));
  expect(rows()[1]).toHaveTextContent('agent');
  fireEvent.click(screen.getByRole('radio', { name: 'Flat' }));
  expect(screen.getByLabelText('Location')).toHaveTextContent('activity=resources');
  expect(screen.getByLabelText('Location')).toHaveTextContent('remoteId=box');
  expect(screen.getByLabelText('Location')).not.toHaveTextContent('resourceView');
  expect(rows()[1]).not.toHaveAttribute('data-depth');
  fireEvent.click(screen.getByRole('button', { name: 'Back' }));
  expect(screen.getByRole('radio', { name: 'Tree' })).toBeChecked();
});

it('switches from flat to tree and displays known project and session names', async () => {
  vi.mocked(resources).mockResolvedValue({ ...snapshot, processes: [
    snapshot.processes[0],
    { ...snapshot.processes[1], sessionId: 'session-one', sessionTitle: 'Build process tree', projectName: 'ocman' },
  ] });
  renderViewer('/?activity=resources');
  await screen.findByRole('table');
  fireEvent.click(screen.getByRole('radio', { name: 'Tree' }));
  expect(screen.getByLabelText('Location')).toHaveTextContent('resourceView=tree');
  expect(within(screen.getByRole('table')).getByText('ocman', { selector: 'td' })).toBeVisible();
  expect(screen.getByRole('link', { name: 'Build process tree' })).toHaveAttribute('href', '/session/session-one');
});

it('filters the list and graph totals by project and session, preserving URL scope and Back', async () => {
  const processes = [
    snapshot.processes[0],
    { ...snapshot.processes[1], sessionId: 'one', sessionTitle: 'Build UI', projectName: 'ocman' },
    { pid: 3, parentPid: 2, name: 'worker', cpuPercent: 50, memoryBytes: 0, sessionId: 'one', projectName: 'ocman' },
    { pid: 4, parentPid: 1, name: 'other', cpuPercent: 10, memoryBytes: 0, sessionId: 'two', sessionTitle: 'Fix tests', projectName: 'tools' },
  ];
  const rawProcesses = processes.map(({ sessionId: _session, sessionTitle: _title, projectName: _project, ...process }) => process);
  vi.mocked(resources).mockResolvedValue({ ...snapshot, processes, history: [{ ...snapshot.history[1], processes: rawProcesses }] });
  renderViewer('/?activity=resources&remoteId=box');
  await screen.findByRole('table');
  expect(screen.getByRole('columnheader', { name: 'Session' })).toBeVisible();
  expect(screen.getByRole('columnheader', { name: 'Project' })).toBeVisible();
  fireEvent.change(screen.getByLabelText('Filter by project'), { target: { value: 'ocman' } });
  expect(screen.getByText('2 processes', { exact: false })).toBeVisible();
  expect(screen.getByText('175.0%')).toBeVisible();
  expect(screen.queryByRole('button', { name: 'Select other, PID 4' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'CPU: ocman, PID 1' })).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Filter by session'), { target: { value: 'two' } });
  expect(screen.getByText('No processes match these filters.')).toBeVisible();
  expect(screen.getByLabelText('Location')).toHaveTextContent('resourceProject=ocman');
  expect(screen.getByLabelText('Location')).toHaveTextContent('resourceSession=two');
  fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
  expect(screen.getByRole('button', { name: 'Select other, PID 4' })).toBeVisible();
  expect(screen.getByLabelText('Location')).toHaveTextContent('remoteId=box');
  expect(screen.getByLabelText('Location')).toHaveTextContent('activity=resources');
  fireEvent.click(screen.getByRole('button', { name: 'Back' }));
  expect(screen.getByLabelText('Filter by session')).toHaveValue('two');
});

it('restores session filters and keeps their options when processes exit', async () => {
  const process = { ...snapshot.processes[1], sessionId: 'one', projectName: 'ocman' };
  vi.mocked(resources).mockResolvedValue({ ...snapshot, processes: [process], history: [{ ...snapshot.history[1], processes: [process] }] });
  const { client } = renderViewer('/?resourceSession=one&resourceProcess=1%3A0%3Aocman&resourceView=tree');
  await screen.findByRole('table');
  expect(screen.getByLabelText('Filter by session')).toHaveValue('one');
  expect(screen.getByRole('option', { name: 'one' })).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Open session' })).toBeVisible();
  fireEvent.change(screen.getByLabelText('Filter by project'), { target: { value: 'ocman' } });
  expect(screen.getByLabelText('Location')).not.toHaveTextContent('resourceProcess');
  vi.mocked(resources).mockResolvedValue({ ...snapshot, processes: [], history: [] });
  await act(() => client.refetchQueries({ queryKey: ['resources', 'local'] }));
  expect(await screen.findByText('No processes match these filters.')).toBeVisible();
  expect(screen.getByLabelText('Filter by session')).toHaveValue('one');
  expect(screen.getByLabelText('Filter by project')).toHaveValue('ocman');
});

it('retains known historical attribution after a process exits without attributing a reused PID', async () => {
  const process = { ...snapshot.processes[1], sessionId: 'one', sessionTitle: 'Build UI', projectName: 'ocman' };
  const raw = snapshot.processes[1];
  vi.mocked(resources).mockResolvedValue({ ...snapshot, processes: [process], history: [{ ...snapshot.history[1], processes: [raw] }] });
  const { client } = renderViewer('/?resourceSession=one');
  await screen.findByRole('table');
  expect(screen.getByRole('button', { name: 'CPU: agent, PID 2' })).toBeVisible();
  vi.mocked(resources).mockResolvedValue({ ...snapshot, processes: [{ ...raw, name: 'replacement' }], history: [
    { ...snapshot.history[1], processes: [raw] },
    { timestamp: 12000, cpuPercent: 999, memoryBytes: 0, processes: [{ ...raw, name: 'replacement', cpuPercent: 999 }] },
  ] });
  await act(() => client.refetchQueries({ queryKey: ['resources', 'local'] }));
  expect(await screen.findByText('No processes match these filters.')).toBeVisible();
  expect(screen.getByRole('button', { name: 'CPU: agent, PID 2' })).toBeVisible();
  expect(screen.queryByRole('button', { name: 'CPU: replacement, PID 2' })).not.toBeInTheDocument();
  expect(screen.getByRole('option', { name: 'Build UI' })).toBeInTheDocument();
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

it('polls every two seconds, pauses when hidden and aborts when unmounted', async () => {
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
  expect(resources).toHaveBeenCalledTimes(1);
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(resources).toHaveBeenCalledTimes(2);
  vi.mocked(resources).mockReturnValue(new Promise(() => {}));
  await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
  const signal = vi.mocked(resources).mock.calls.at(-1)![1];
  page.unmount();
  expect(signal.aborted).toBe(true);
  await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
  expect(resources).toHaveBeenCalledTimes(3);
});

it('freezes list samples while paused, keeps graphs live and resumes with the latest sample', async () => {
  const { client } = renderViewer();
  await screen.findByRole('table');
  fireEvent.click(screen.getByRole('button', { name: 'Pause list updates' }));
  const latest = { ...snapshot, cpuPercent: 999, processes: [{ ...snapshot.processes[0], name: 'new process' }], history: [{ ...snapshot.history[0], cpuPercent: 999 }] };
  vi.mocked(resources).mockResolvedValue(latest);
  await act(() => client.refetchQueries({ queryKey: ['resources', 'local'] }));
  expect(await screen.findByText('999.0%')).toBeVisible();
  expect(within(screen.getByRole('table')).getByText('agent')).toBeVisible();
  expect(within(screen.getByRole('table')).queryByText('new process')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Resume list updates' }));
  expect(within(screen.getByRole('table')).getByText('new process')).toBeVisible();
  expect(within(screen.getByRole('table')).queryByText('agent')).not.toBeInTheDocument();
});

it('hides the machine selector when no remotes are configured and reads this machine', async () => {
  renderViewer();
  await screen.findByRole('table', { name: 'Ocman processes' });
  expect(screen.queryByRole('combobox', { name: 'Machine' })).toBeNull();
  expect(screen.getByText(/^This machine · refreshes/)).toBeVisible();
  expect(resources).toHaveBeenCalledWith('local', expect.any(AbortSignal));
});

it('switches to a remote machine, clears machine-scoped filters and restores it from the URL', async () => {
  mockRemotes([
    { remoteId: 'build', displayName: 'Build box' },
    { remoteId: 'laptop', hostname: 'laptop.lan', health: 'connecting' },
    { displayName: 'Never connected' },
  ]);
  renderViewer('/?resourceProject=ocman&resourceSession=one&resourceProcess=1&resourceSort=pid');
  const select = await screen.findByRole('combobox', { name: 'Machine' });
  expect(within(select).getAllByRole('option').map((option) => option.textContent)).toEqual(['This machine', 'Build box', 'laptop.lan (offline)']);
  expect(select).toHaveValue('local');
  fireEvent.change(select, { target: { value: 'build' } });
  expect(screen.getByLabelText('Location').textContent).toBe('?resourceSort=pid&resourceMachine=build');
  await screen.findByText(/^Build box · refreshes/);
  expect(resources).toHaveBeenLastCalledWith('build', expect.any(AbortSignal));
  expect(screen.queryByText(/Backend uptime/)).toBeNull();
  fireEvent.change(select, { target: { value: 'local' } });
  expect(screen.getByLabelText('Location').textContent).toBe('?resourceSort=pid');
});

it('keeps an unknown machine from the URL selected and fails closed instead of showing this machine', async () => {
  mockRemotes([{ remoteId: 'build', displayName: 'Build box' }]);
  vi.mocked(resources).mockImplementation((machine) => machine === 'gone' ? Promise.reject(new Error('remote gone is not connected')) : Promise.resolve(snapshot));
  renderViewer('/?resourceMachine=gone');
  const select = await screen.findByRole('combobox', { name: 'Machine' });
  expect(select).toHaveValue('gone');
  expect(within(select).getByRole('option', { name: 'gone (offline)' })).toBeInTheDocument();
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load process resources.');
  expect(screen.queryByRole('table')).toBeNull();
  expect(resources).not.toHaveBeenCalledWith('local', expect.anything());
});
