// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import { PluginPageNav, PluginPageTitle } from './PluginPageNav';

const mocks = vi.hoisted(() => ({ pages: [] as unknown[], uis: [] as unknown[] }));
vi.mock('../lib/pluginPages', async (original) => ({ ...await original<typeof import('../lib/pluginPages')>(),
  usePluginPages: () => ({ pages: mocks.pages, loading: false, error: false }) }));
vi.mock('../lib/pluginUI', async (original) => ({ ...await original<typeof import('../lib/pluginUI')>(), usePluginUIs: () => ({ uis: mocks.uis, loading: false, error: false }) }));

beforeEach(() => {
  mocks.uis = [{ pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'local', sdk: '^1.0.0', hostSdk: '1.0.0', compatible: true, index: { url: '/bundle', sha256: 'abc' }, actions: [] }];
  mocks.pages = [
    { pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'local', page: { id: 'settings', label: 'Settings' } },
    { pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'machine', page: { id: 'settings', label: 'Settings' } },
  ];
});

it('links hub-local UI plugins to their index and remote owners to declarative pages', () => {
  render(<MemoryRouter initialEntries={['/plugins/org.example.catalog/items/7']}><nav><PluginPageNav /></nav><header><PluginPageTitle /></header></MemoryRouter>);
  const local = screen.getByRole('link', { name: 'Catalog (local)' });
  expect(local).toHaveAttribute('href', '/plugins/org.example.catalog');
  expect(local).toHaveAttribute('aria-current', 'page');
  expect(screen.getByRole('link', { name: 'Catalog (machine)' })).toHaveAttribute('href', '/plugins/org.example.catalog/settings?ownerId=machine');
  expect(screen.getByRole('banner')).toHaveTextContent('Catalog');
});

it('falls back to a generic title outside known plugins', () => {
  mocks.uis = [];
  mocks.pages = [];
  render(<MemoryRouter initialEntries={['/plugins/org.example.unknown']}><PluginPageTitle /></MemoryRouter>);
  expect(screen.getByText('Plugin workspace')).toBeInTheDocument();
});

it('keeps declarative navigation for plugins whose interface cannot load', () => {
  mocks.uis = [{ ...(mocks.uis[0] as object), compatible: false }];
  render(<MemoryRouter><PluginPageNav /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Catalog (local)' })).toHaveAttribute('href', '/plugins/org.example.catalog/settings?ownerId=local');
});
