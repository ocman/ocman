// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';
import { expect, it, vi } from 'vitest';
import * as PluginUI from './index';
import { PluginHostContext, type PluginHostValue } from './context';
import { ConfirmProvider } from '../components/ConfirmDialog';
import { PluginPageTitle } from '../components/PluginPageNav';
import { usePluginTitles } from '../lib/pluginUI';

vi.mock('../lib/pluginPages', async (original) => ({ ...await original<typeof import('../lib/pluginPages')>(),
  usePluginPages: () => ({ pages: [], loading: false, error: false, failedOwners: [], discoveryError: false, refetch: vi.fn() }) }));
vi.mock('../lib/pluginUI', async (original) => ({ ...await original<typeof import('../lib/pluginUI')>(),
  usePluginUIs: () => ({ uis: [{ pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'local', compatible: true, index: { url: '/b', sha256: 'x' }, actions: [] }], loading: false, error: false }) }));

const host: PluginHostValue = { pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'local', basePath: '/plugins/org.example.catalog', runAction: () => Promise.resolve() };

function mount(children: ReactNode, path = '/plugins/org.example.catalog') {
  return render(<QueryClientProvider client={new QueryClient()}><MemoryRouter initialEntries={[path]}><ConfirmProvider>
    <header><PluginPageTitle /></header>
    <Routes><Route path="/plugins/:pluginId/*" element={<PluginHostContext.Provider value={host}>{children}</PluginHostContext.Provider>} /><Route path="*" element={<p>Left the plugin</p>} /></Routes>
  </ConfirmProvider></MemoryRouter></QueryClientProvider>);
}

it('renders page states with owned icons, inline refresh warnings and skeletons', () => {
  const retry = vi.fn();
  const { container } = mount(<>
    <PluginUI.PageState title="No items yet" description="Add an item to get started."><button>Add item</button></PluginUI.PageState>
    <PluginUI.PageState error title="Items unavailable" description="The plugin did not answer." icon="cloud-slash" />
    <PluginUI.PageState title="Bad icon" description="Falls back." icon="x onerror" />
    <PluginUI.InlineAlert onRetry={retry}>Could not refresh items.</PluginUI.InlineAlert>
    <PluginUI.SkeletonTable columns={['Name', 'Status']} rows={2} label="Loading items" />
    <PluginUI.SkeletonLines label="Loading notes" />
    <PluginUI.Icon name="star" label="Favorite" />
    <PluginUI.Icon name="bad name" />
  </>);
  expect(screen.getByRole('region', { name: 'No items yet' }).querySelector('.bi-inbox')).not.toBeNull();
  expect(screen.getByRole('button', { name: 'Add item' })).toBeInTheDocument();
  const unavailable = screen.getAllByRole('alert').find((el) => el.textContent?.includes('Items unavailable'))!;
  expect(unavailable.querySelector('.bi-cloud-slash')).not.toBeNull();
  expect(screen.getByRole('region', { name: 'Bad icon' }).querySelector('.bi-inbox')).not.toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(retry).toHaveBeenCalledOnce();
  expect(screen.getByRole('table', { name: 'Loading items' })).toHaveAttribute('aria-busy', 'true');
  expect(screen.getByLabelText('Loading notes')).toHaveAttribute('aria-busy', 'true');
  expect(screen.getByRole('img', { name: 'Favorite' })).toHaveClass('bi', 'bi-star');
  expect(container.querySelectorAll('i.bi')).toHaveLength(4);
});

it('shows relative time with the shared exact date and stacked list rows', () => {
  vi.useFakeTimers({ now: new Date(2026, 9, 28, 12) });
  try {
    mount(<>
      <PluginUI.RelativeTime value={new Date(2026, 9, 28, 10).toISOString()} />
      <PluginUI.RelativeTime value="invalid" />
      <p>{PluginUI.formatExactDate(new Date(2025, 0, 2, 7))}</p>
      <PluginUI.DataTableGroup label="Active" noun="items" count={1}>
        <PluginUI.DataTableRow primary={<a href="#lamp">Desk lamp</a>} secondary="Furniture" meta="Facilities" />
      </PluginUI.DataTableGroup>
    </>);
    const time = screen.getByText('2h ago').closest('time')!;
    expect(time).toHaveAttribute('tabindex', '0');
    expect(time).toHaveAttribute('aria-label', '2h ago, Oct 28, 10:00');
    expect(screen.getByText('Jan 2, 2025, 07:00')).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'Active items' })).toHaveTextContent('Furniture');
    expect(screen.getAllByRole('listitem')[0]).toHaveClass('oc-data-table-row');
  } finally { vi.useRealTimers(); }
});

it('resolves route tabs from the plugin root at any depth', () => {
  mount(<PluginUI.RouteTabs label="Views" tabs={[{ to: '', label: 'Overview', end: true }, { to: 'archive', label: 'Archive' }, { to: '/settings', label: 'Settings' }]} />,
    '/plugins/org.example.catalog/items/7?q=lamp');
  expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute('href', '/plugins/org.example.catalog?q=lamp');
  expect(screen.getByRole('link', { name: 'Overview' })).not.toHaveAttribute('aria-current');
  expect(screen.getByRole('link', { name: 'Archive' })).toHaveAttribute('href', '/plugins/org.example.catalog/archive?q=lamp');
  expect(screen.getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/settings?q=lamp');
});

it('sets the header title instead of rendering a second heading', () => {
  function Detail() {
    const [name, setName] = useState('Desk lamp');
    return <PluginUI.Page title={name} description="One item" actions={<button onClick={() => setName('Lamp')}>Rename</button>}>Body</PluginUI.Page>;
  }
  const view = mount(<Detail />);
  expect(screen.getByRole('banner')).toHaveTextContent('Catalog / Desk lamp');
  expect(document.title).toBe('Catalog / Desk lamp - ocman');
  expect(screen.queryByRole('heading', { level: 1 })).toBeNull();
  expect(screen.getByRole('region', { name: 'Desk lamp' })).toHaveTextContent('One item');
  fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
  expect(screen.getByRole('banner')).toHaveTextContent('Catalog / Lamp');
  view.unmount();
  expect(usePluginTitles.getState().titles).toEqual({});
});

it('keeps the plugin name alone when the page title repeats it', () => {
  mount(<PluginUI.Page title="Catalog">Body</PluginUI.Page>);
  expect(screen.getByRole('banner')).toHaveTextContent(/^Catalog$/);
});

it('exposes confirmation and unsaved-change protection', async () => {
  let answer: boolean | undefined;
  function Editor() {
    const confirm = PluginUI.useConfirm();
    const { confirmDiscard } = PluginUI.useUnsavedChangesGuard(true);
    return <>
      <button onClick={() => void confirm({ title: 'Delete item Desk lamp?', confirmLabel: 'Delete item', destructive: true }).then((ok) => { answer = ok; })}>Delete</button>
      <button onClick={() => void confirmDiscard()}>Close editor</button>
      <a href="/sessions" onClick={(e) => e.preventDefault()}>Leave</a>
    </>;
  }
  mount(<Editor />);
  fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
  expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();
  fireEvent.click(screen.getByRole('button', { name: 'Delete item' }));
  await waitFor(() => expect(answer).toBe(true));
  fireEvent.click(screen.getByRole('button', { name: 'Close editor' }));
  expect(screen.getByRole('dialog', { name: 'Discard unsaved changes?' })).toBeInTheDocument();
  await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Keep editing' })); });
});
