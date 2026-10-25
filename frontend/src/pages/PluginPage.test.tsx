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

function mount(path = '/plugins/org.example.pentest/pentest?ownerId=machine') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[path]}>
    <Routes><Route path="/plugins/:pluginId/:pageId" element={<PluginPage />} /></Routes>
  </MemoryRouter></QueryClientProvider>);
  return client;
}

it('reads a plugin-local homepage once without enumerating project configurations', async () => {
  mocks.pages = [{ pluginId: 'org.example.pentest', ownerId: 'machine', page: { id: 'pentest', label: 'Pentest', scope: 'plugin' } }];
  mocks.call.mockResolvedValue({ revision: 'r1', fields: [], values: {}, tree: {}, collection: {
    title: 'Saved targets', columns: [{ key: 'name', label: 'Name', display: 'text' }],
    rows: [{ id: 'config-a', projectId: 'project-a', values: { name: 'Staging targets' } }],
  } });
  mount();
  expect(await screen.findByText('Staging targets')).toBeInTheDocument();
  expect(mocks.sessions).not.toHaveBeenCalled();
  expect(mocks.actions).not.toHaveBeenCalled();
  expect(mocks.call).toHaveBeenCalledTimes(1);
  expect(mocks.call).toHaveBeenCalledWith(mocks.pages[0], '', 'read', undefined, expect.any(AbortSignal));
  expect(screen.getByRole('link', { name: 'View config-a' })).toHaveAttribute('href', '/plugins/org.example.pentest/pentest?ownerId=machine&projectId=project-a');
  fireEvent.click(screen.getByRole('link', { name: 'View config-a' }));
  await waitFor(() => expect(mocks.call).toHaveBeenLastCalledWith(mocks.pages[0], 'project-a', 'read', undefined, expect.any(AbortSignal)));
  fireEvent.click(screen.getByRole('button', { name: 'Back to configurations' }));
  expect(await screen.findByText('Staging targets')).toBeInTheDocument();
  expect(mocks.sessions).not.toHaveBeenCalled();
});

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
  await screen.findByRole('button', { name: 'Back to configurations' });
  expect(screen.queryByRole('button', { name: 'Save configuration' })).not.toBeInTheDocument();
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

it('keeps the draft and shows validation issues without treating them as a save', async () => {
  const client = mount();
  fireEvent.click(await screen.findByRole('button', { name: 'Open configuration for srv/project' }));
  fireEvent.change(await screen.findByLabelText('Target name'), { target: { value: 'taken' } });
  mocks.call.mockResolvedValue({ revision: 'r1', fields: [], values: {}, tree: {}, issues: [{ path: '/name', message: 'Name is already used.' }] });
  fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));
  await screen.findByText('Fix the validation errors before saving.');
  expect(screen.getByLabelText('Target name')).toHaveValue('taken');
  expect(screen.getByRole('alert')).toHaveTextContent('Name is already used.');
  expect(screen.queryByText('Saved.')).not.toBeInTheDocument();
  expect(client.getQueryData(['plugin-page-document', 'machine', 'org.example.pentest', 'pentest', 'project-a'])).toMatchObject({ revision: 'r1', values: { name: 'staging' } });
  fireEvent.change(screen.getByLabelText('Target name'), { target: { value: 'available' } });
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('opens owner-qualified list details and passes opaque pagination cursors', async () => {
  const collection = { title: 'Jobs', columns: [{ key: 'name', label: 'Name', display: 'text' }], rows: [{ id: 'job-1', values: { name: '<script>inert</script>' } }], nextCursor: 'next-1' };
  mocks.call.mockImplementation((_page, _project, _method, _doc, _signal, context) => Promise.resolve(context?.itemId
    ? { revision: 'r1', fields: [], values: {}, tree: {}, detail: { title: 'Job details', properties: [{ label: 'Name', display: 'text', value: 'Example job' }] } }
    : { revision: 'r1', fields: [], values: {}, tree: {}, collection }));
  mount('/plugins/org.example.pentest/pentest?ownerId=machine&projectId=project-a');
  expect(await screen.findByText('<script>inert</script>')).toBeInTheDocument();
  expect(document.querySelector('script')).toBeNull();
  expect(screen.getByRole('link', { name: 'View job-1' })).toHaveAttribute('href', '/plugins/org.example.pentest/pentest?ownerId=machine&projectId=project-a&itemId=job-1');
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
  await waitFor(() => expect(mocks.call).toHaveBeenLastCalledWith(mocks.pages[0], 'project-a', 'read', undefined, expect.any(AbortSignal), { cursor: 'next-1' }));
  fireEvent.click(await screen.findByRole('button', { name: 'First page' }));
  fireEvent.click(await screen.findByRole('link', { name: 'View job-1' }));
  expect(await screen.findByText('Example job')).toBeInTheDocument();
  expect(mocks.call).toHaveBeenLastCalledWith(mocks.pages[0], 'project-a', 'read', undefined, expect.any(AbortSignal), { itemId: 'job-1' });
  fireEvent.click(screen.getByRole('button', { name: 'Back to list' }));
  expect(await screen.findByRole('link', { name: 'View job-1' })).toBeInTheDocument();
});

it('offers a way back when a deep-linked item cannot be read', async () => {
  mocks.call.mockRejectedValue(new Error('not_found'));
  mount('/plugins/org.example.pentest/pentest?ownerId=machine&projectId=project-a&itemId=missing');
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not read this workspace or item.');
  expect(screen.getByRole('button', { name: 'Back to list' })).toBeInTheDocument();
});

it('resets to the first page after saving without overwriting a cursor-qualified cache', async () => {
  let saved = false;
  const collection = (name: string) => ({ title: 'Jobs', columns: [{ key: 'name', label: 'Name', display: 'text' }], rows: [{ id: 'job-1', values: { name } }], nextCursor: 'next-1' });
  mocks.call.mockImplementation((_page, _project, method, _doc, _signal, context) => {
    if (method === 'save') { expect(context).not.toHaveProperty('cursor'); saved = true; }
    return Promise.resolve({ revision: saved ? 'r2' : 'r1', fields: [{ key: 'name', label: 'Job name', type: 'text' }], values: { name: saved ? 'edited' : 'original' }, tree: {},
      collection: collection(context?.cursor ? 'Second page row' : saved ? 'First page updated' : 'First page row') });
  });
  const client = mount('/plugins/org.example.pentest/pentest?ownerId=machine&projectId=project-a');
  await screen.findByText('First page row');
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
  await screen.findByText('Second page row');
  fireEvent.change(screen.getByLabelText('Job name'), { target: { value: 'edited' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));
  await screen.findByText('Saved.');
  expect(screen.queryByRole('button', { name: 'First page' })).not.toBeInTheDocument();
  expect(screen.getByText('First page updated')).toBeInTheDocument();
  expect(client.getQueryData(['plugin-page-document', 'machine', 'org.example.pentest', 'pentest', 'project-a'])).toMatchObject({ revision: 'r2', collection: collection('First page updated') });
});
