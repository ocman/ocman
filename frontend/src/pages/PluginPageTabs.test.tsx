// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, expect, it, vi } from 'vitest';
import { PluginPage } from './PluginPage';
import { PluginPageNav, PluginPageTitle } from '../components/PluginPageNav';

const mocks = vi.hoisted(() => ({ pages: [] as unknown[], call: vi.fn() }));
vi.mock('../lib/pluginPages', async (original) => ({ ...await original<typeof import('../lib/pluginPages')>(),
  usePluginPages: () => ({ pages: mocks.pages, loading: false }), callPluginPage: mocks.call,
}));
vi.mock('../lib/queries', () => ({ useProjects: () => ({ data: [] }) }));

beforeEach(() => {
  mocks.call.mockReset().mockResolvedValue({ revision: 'r1', fields: [], values: {}, tree: {} });
  mocks.pages = ['workspaces', 'runs'].map((id) => ({ ownerId: 'local', pluginId: 'example.pentest', pluginLabel: 'Pentest', page: { id, label: id, scope: 'plugin' } }));
});

function mount(path = '/plugins/example.pentest/runs?ownerId=local&itemId=old&projectId=project-a') {
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <MemoryRouter initialEntries={[path]}><nav><PluginPageNav /></nav><header><PluginPageTitle /></header>
      <Routes><Route path="/plugins/:pluginId/:pageId" element={<PluginPage />} /></Routes>
    </MemoryRouter>
  </QueryClientProvider>);
}

it('groups root pages under one plugin link and keeps it active on any tab', async () => {
  mount();
  expect(screen.getAllByRole('link', { name: 'Pentest' })).toHaveLength(1);
  expect(screen.getByRole('link', { name: 'Pentest' })).toHaveClass('active');
  expect(screen.getByRole('link', { name: 'Pentest' })).toHaveAttribute('aria-current', 'page');
  expect(screen.getByRole('tab', { name: 'runs' })).toHaveAttribute('aria-selected', 'true');
  expect(screen.getByRole('tabpanel')).toHaveAccessibleName('runs');
  expect(screen.getByRole('banner')).toHaveTextContent('Pentest');
  await waitFor(() => expect(mocks.call).toHaveBeenCalledTimes(1));
  await userEvent.click(screen.getByRole('tab', { name: 'workspaces' }));
  await waitFor(() => expect(mocks.call).toHaveBeenLastCalledWith(mocks.pages[0], '', 'read', undefined, expect.any(AbortSignal)));
  expect(screen.getByRole('tab', { name: 'workspaces' })).toHaveAttribute('aria-selected', 'true');
  expect(screen.getByRole('link', { name: 'Pentest' })).toHaveClass('active');
});

it('keeps different owners separate and never shows another owner\'s pages as tabs', () => {
  mocks.pages.push({ ownerId: 'remote', pluginId: 'example.pentest', pluginLabel: 'Pentest', page: { id: 'remote-only', label: 'Remote only', scope: 'plugin' } });
  mount('/plugins/example.pentest/runs?ownerId=local');
  expect(screen.getByRole('link', { name: 'Pentest (local)' })).toHaveClass('active');
  expect(screen.getByRole('link', { name: 'Pentest (remote)' })).not.toHaveClass('active');
  expect(screen.queryByRole('tab', { name: 'Remote only' })).not.toBeInTheDocument();
});

it('does not add tabs for a single declared page', () => {
  mocks.pages.pop();
  mount('/plugins/example.pentest/workspaces?ownerId=local');
  expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
});
