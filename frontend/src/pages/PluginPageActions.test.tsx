// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import { PluginPage } from './PluginPage';

const mocks = vi.hoisted(() => ({ list: vi.fn() }));
vi.mock('../lib/pluginPages', async (original) => ({ ...await original<typeof import('../lib/pluginPages')>(),
  usePluginPages: () => ({ pages: [{ ownerId: 'local', pluginId: 'org.example.inventory', page: { id: 'inventory', label: 'Inventory', scope: 'plugin' } }], loading: false }),
  callPluginPage: async () => ({ revision: 'v1', fields: [], values: {}, tree: {} }),
}));
vi.mock('../lib/queries', () => ({ useProjects: () => ({ data: [] }) }));
vi.mock('../lib/plugins', async (original) => ({ ...await original<typeof import('../lib/plugins')>(), pluginActions: { list: mocks.list } }));

it('treats action identifiers as opaque and only filters by the declaring plugin', async () => {
  mocks.list.mockResolvedValue([
    { pluginId: 'org.example.inventory', action: { id: 'project-config', label: 'Describe inventory configuration' } },
    { pluginId: 'org.example.other', action: { id: 'different-action', label: 'Other plugin action' } },
  ]);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/plugins/org.example.inventory/inventory?ownerId=local&itemId=workspace-a']}>
    <Routes><Route path="/plugins/:pluginId/:pageId" element={<PluginPage />} /></Routes>
  </MemoryRouter></QueryClientProvider>);
  expect(await screen.findByRole('button', { name: 'Describe inventory configuration' })).toBeEnabled();
  expect(screen.queryByRole('button', { name: 'Other plugin action' })).not.toBeInTheDocument();
  expect(mocks.list).toHaveBeenCalledWith('plugin', { ownerId: 'local', workspaceId: 'workspace-a' }, expect.any(AbortSignal));
});
