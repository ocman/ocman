// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, expect, it, vi } from 'vitest';
import { PluginPage } from './PluginPage';
import { PluginPageNav } from '../components/PluginPageNav';

const mocks = vi.hoisted(() => ({ pages: [] as unknown[], call: vi.fn(), sessions: vi.fn(), actions: vi.fn() }));
vi.mock('../lib/pluginPages', async (original) => ({ ...await original<typeof import('../lib/pluginPages')>(),
  usePluginPages: () => ({ pages: mocks.pages, loading: false, error: false }), callPluginPage: mocks.call,
}));
vi.mock('../lib/queries', () => ({ useProjects: () => ({ data: [{ directory: '/srv/project', remoteId: 'machine' }] }) }));
vi.mock('../lib/api', async (original) => ({ ...await original<typeof import('../lib/api')>(), api: { sessions: mocks.sessions } }));
vi.mock('../lib/plugins', async (original) => ({ ...await original<typeof import('../lib/plugins')>(), pluginActions: { list: mocks.actions } }));

beforeEach(() => {
  vi.clearAllMocks();
  mocks.pages = [{ pluginId: 'org.example.pentest', ownerId: 'machine', page: { id: 'pentest', label: 'Pentest' } }];
  mocks.sessions.mockResolvedValue([{ directory: '/srv/project', remoteId: 'machine', projectId: 'project-a' }]);
  mocks.actions.mockResolvedValue([]);
  mocks.call.mockResolvedValue({ revision: 'r1', fields: [{ key: 'name', label: 'Target name', type: 'text' }, { key: 'pass', label: 'Password', type: 'secret' }], values: { name: 'staging', pass: '' }, tree: { available: true } });
});

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/plugins/org.example.pentest/pentest?ownerId=machine']}>
    <Routes><Route path="/plugins/:pluginId/:pageId" element={<PluginPage />} /></Routes>
  </MemoryRouter></QueryClientProvider>);
  return client;
}

it('routes reads and saves to the selected owner and clears passwords after save', async () => {
  const client = mount();
  expect(screen.queryByLabelText('Project')).not.toBeInTheDocument();
  fireEvent.click(await screen.findByRole('button', { name: 'Open configuration for srv/project' }));
  await screen.findByLabelText('Target name');
  expect(mocks.call).toHaveBeenCalledWith(mocks.pages[0], 'project-a', 'read', undefined, expect.any(AbortSignal));
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'private-write' } });
  fireEvent.change(screen.getByLabelText('Target name'), { target: { value: 'production' } });
  mocks.call.mockResolvedValue({ revision: 'r2', fields: [{ key: 'pass', label: 'Password', type: 'secret' }], values: { pass: '' }, tree: { available: true } });
  fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));
  await screen.findByText('Saved.');
  expect(screen.getByLabelText('Password')).toHaveValue('');
  expect(mocks.call.mock.calls.find((call) => call[2] === 'save')?.[3].values).toEqual({ name: 'production', pass: 'private-write' });
  expect(JSON.stringify(client.getQueryCache().getAll().map((q) => q.state.data))).not.toContain('private-write');
});

it('does not overwrite an edit or retry a failed save', async () => {
  mount();
  fireEvent.click(await screen.findByRole('button', { name: 'Open configuration for srv/project' }));
  fireEvent.change(await screen.findByLabelText('Target name'), { target: { value: 'unsaved' } });
  mocks.call.mockRejectedValue(new Error('conflict'));
  fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));
  await screen.findByText(/Save failed/);
  expect(screen.getByLabelText('Target name')).toHaveValue('unsaved');
  expect(mocks.call.mock.calls.filter((call) => call[2] === 'save')).toHaveLength(1);
});

it('renders a results-only document with null fields', async () => {
  mocks.call.mockResolvedValue({ revision: 'r1', fields: null, values: {}, tree: { available: true, nodes: [{ id: 'result', title: 'Saved finding' }] } });
  mount();
  fireEvent.click(await screen.findByRole('button', { name: 'Open configuration for srv/project' }));
  expect(await screen.findByText('Saved finding')).toBeInTheDocument();
});

it('keeps saved values and revision when an older polling read finishes late', async () => {
  const client = mount();
  fireEvent.click(await screen.findByRole('button', { name: 'Open configuration for srv/project' }));
  fireEvent.change(await screen.findByLabelText('Target name'), { target: { value: 'production' } });
  const key = ['plugin-page-document', 'machine', 'org.example.pentest', 'pentest', 'project-a'];
  const old = client.getQueryData(key) as { revision: string; values: object };
  let finishRead!: (value: unknown) => void;
  mocks.call.mockImplementation((_page, _project, method) => method === 'read'
    ? new Promise(resolve => { finishRead = resolve; })
    : Promise.resolve({ ...old, revision: 'r2', values: { name: 'production', pass: '' } }));
  let pendingRead!: Promise<void>;
  act(() => { pendingRead = client.refetchQueries({ queryKey: key, exact: true }); });
  fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));
  await screen.findByText('Saved.');
  await act(async () => { finishRead(old); await pendingRead; });
  expect(screen.getByLabelText('Target name')).toHaveValue('production');
  expect(client.getQueryData(key)).toMatchObject({ revision: 'r2', values: { name: 'production' } });
});

it('hides unavailable plugin links and refuses a direct route', async () => {
  mocks.pages = [];
  mount();
  await screen.findByText(/Plugin unavailable/);
  expect(mocks.call).not.toHaveBeenCalled();
  render(<MemoryRouter><PluginPageNav /></MemoryRouter>);
  expect(screen.queryByRole('link', { name: 'Pentest' })).not.toBeInTheDocument();
});

it('lists only saved configurations and uses the project picker only when creating one', async () => {
  mocks.call.mockResolvedValue({ configured: false, revision: 'r1', fields: [], values: {}, tree: {} });
  mount();
  await screen.findByText(/No saved configurations/);
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'New configuration' }));
  fireEvent.click(screen.getByRole('combobox', { name: 'Configuration project' }));
  fireEvent.click(screen.getByRole('option', { name: 'srv/project' }));
  await screen.findByRole('button', { name: 'Save configuration' });
  fireEvent.click(screen.getByRole('button', { name: 'Back to configurations' }));
  await screen.findByText(/No saved configurations/);
});

it('adds an owner-qualified navigation link', async () => {
  render(<MemoryRouter><PluginPageNav /></MemoryRouter>);
  await waitFor(() => expect(screen.getByRole('link', { name: 'Pentest' })).toHaveAttribute('href', '/plugins/org.example.pentest/pentest?ownerId=machine'));
});

it('distinguishes identical plugin pages on different owners', () => {
  mocks.pages.push({ pluginId: 'org.example.pentest', ownerId: 'local', page: { id: 'pentest', label: 'Pentest' } });
  render(<MemoryRouter initialEntries={['/plugins/org.example.pentest/pentest?ownerId=machine']}><PluginPageNav /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Pentest (machine)' })).toHaveClass('active');
  expect(screen.getByRole('link', { name: 'Pentest (local)' })).not.toHaveClass('active');
});
