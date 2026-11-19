// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import { PluginPageNav, PluginPageTitle } from './PluginPageNav';
import { fetchJSON } from '../lib/api';
import { renderWithQuery } from '../test/renderWithQuery';

const mocks = vi.hoisted(() => ({ pages: [] as unknown[], uis: [] as unknown[] }));
vi.mock('../lib/pluginPages', async (original) => ({ ...await original<typeof import('../lib/pluginPages')>(),
  usePluginPages: () => ({ pages: mocks.pages, loading: false, error: false }) }));
vi.mock('../lib/pluginUI', async (original) => ({ ...await original<typeof import('../lib/pluginUI')>(), usePluginUIs: () => ({ uis: mocks.uis, loading: false, error: false }) }));
vi.mock('../lib/api', async (original) => ({ ...await original<typeof import('../lib/api')>(), fetchJSON: vi.fn() }));

beforeEach(() => {
  vi.mocked(fetchJSON).mockReset();
  mocks.uis = [{ pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'local', sdk: '^1.0.0', hostSdk: '1.0.0', compatible: true, index: { url: '/bundle', sha256: 'abc' }, actions: [] }];
  mocks.pages = [
    { pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'local', page: { id: 'settings', label: 'Settings' } },
    { pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'machine', page: { id: 'settings', label: 'Settings' } },
  ];
});

it('updates and clears badges outside the plugin route without mixing owners or unapproved entries', async () => {
  mocks.uis = [{ ...(mocks.uis[0] as object), navBadge: true }];
  mocks.pages = [mocks.pages[0], { ...(mocks.pages[1] as object), navBadge: true },
    { ownerId: 'machine', pluginId: 'org.example.other', page: { id: 'home', label: 'Other' } }];
  let snapshot: Record<string, unknown> = { 'org.example.catalog': { kind: 'count', count: 132 }, 'org.example.other': { kind: 'dot' } };
  let disconnected = false;
  vi.mocked(fetchJSON).mockImplementation(async (url) => {
    if (url.endsWith('ownerId=local')) return { 'org.example.catalog': { kind: 'dot' } };
    if (disconnected) throw new Error('offline');
    return snapshot;
  });
  const { client } = renderWithQuery(<MemoryRouter initialEntries={['/sessions']}><nav><PluginPageNav /></nav></MemoryRouter>);
  const dot = await screen.findByRole('link', { name: 'Catalog (local), notification indicator' });
  expect(dot.querySelector('.plugin-nav-badge--dot')).toBeInTheDocument();
  expect(await screen.findByRole('link', { name: 'Catalog (machine), 132 notifications' })).toHaveTextContent('99+');
  expect(screen.getByRole('link', { name: 'Other' })).toHaveTextContent(/^Other$/);

  const refreshRemote = () => act(async () => { await client.refetchQueries({ queryKey: ['plugin-nav-badges', 'machine'] }); });
  snapshot = { 'org.example.catalog': { kind: 'count', count: 1 } };
  await refreshRemote();
  expect(await screen.findByRole('link', { name: 'Catalog (machine), 1 notification' })).toHaveTextContent('1');
  snapshot = {};
  await refreshRemote();
  expect(await screen.findByRole('link', { name: 'Catalog (machine)' })).toHaveTextContent(/^Catalog \(machine\)$/);

  snapshot = { 'org.example.catalog': { kind: 'dot' } };
  await refreshRemote();
  expect(await screen.findByRole('link', { name: 'Catalog (machine), notification indicator' })).toBeInTheDocument();
  disconnected = true;
  await refreshRemote();
  expect(await screen.findByRole('link', { name: 'Catalog (machine)' })).toBeInTheDocument();
  expect(dot).toHaveAccessibleName('Catalog (local), notification indicator');
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

it('separates plugin links from main navigation with one horizontal rule', () => {
  render(<MemoryRouter><nav><PluginPageNav /></nav></MemoryRouter>);
  const separator = screen.getByRole('separator');
  expect(separator).toHaveClass('main-nav-plugin-separator');
  expect(separator.nextElementSibling).toBe(screen.getByRole('link', { name: 'Catalog (local)' }));
  expect(screen.getAllByRole('separator')).toHaveLength(1);
});

it('omits the separator when there are no plugin links', () => {
  mocks.uis = [];
  mocks.pages = [];
  render(<MemoryRouter><PluginPageNav /></MemoryRouter>);
  expect(screen.queryByRole('separator')).not.toBeInTheDocument();
});

it('keeps declarative navigation for plugins whose interface cannot load', () => {
  mocks.uis = [{ ...(mocks.uis[0] as object), compatible: false }];
  render(<MemoryRouter><PluginPageNav /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Catalog (local)' })).toHaveAttribute('href', '/plugins/org.example.catalog/settings?ownerId=local');
});

it('uses optional icons from both UI and owner-local page catalogs', () => {
  mocks.uis = [{ ...(mocks.uis[0] as object), icon: { bootstrap: 'journal-text' } }];
  mocks.pages = [{ ...(mocks.pages[1] as object), icon: { bootstrap: 'tools' } }];
  render(<MemoryRouter><PluginPageNav /></MemoryRouter>);
  expect(screen.getByRole('link', { name: 'Catalog (local)' }).querySelector('i')).toHaveClass('bi-journal-text');
  expect(screen.getByRole('link', { name: 'Catalog (machine)' }).querySelector('i')).toHaveClass('bi-tools');
});
