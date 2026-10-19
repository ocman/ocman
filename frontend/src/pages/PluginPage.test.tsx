// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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
  fireEvent.change(screen.getByLabelText('Project'), { target: { value: '/srv/project' } });
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
  fireEvent.change(screen.getByLabelText('Project'), { target: { value: '/srv/project' } });
  fireEvent.change(await screen.findByLabelText('Target name'), { target: { value: 'unsaved' } });
  mocks.call.mockRejectedValue(new Error('conflict'));
  fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));
  await screen.findByText(/Save failed/);
  expect(screen.getByLabelText('Target name')).toHaveValue('unsaved');
  expect(mocks.call.mock.calls.filter((call) => call[2] === 'save')).toHaveLength(1);
});

it('hides unavailable plugin links and refuses a direct route', async () => {
  mocks.pages = [];
  mount();
  await screen.findByText(/Plugin unavailable/);
  expect(mocks.call).not.toHaveBeenCalled();
  render(<MemoryRouter><PluginPageNav /></MemoryRouter>);
  expect(screen.queryByRole('link', { name: 'Pentest' })).not.toBeInTheDocument();
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
