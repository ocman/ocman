// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Link, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, expect, it, vi } from 'vitest';
import { PluginRoute } from './PluginRoute';
import type { PluginUI } from '../lib/pluginUI';
import { usePluginAction, usePluginCall, usePluginContext, usePluginMutation } from '../plugin-ui';

const mocks = vi.hoisted(() => ({
  uis: [] as unknown[], pages: [] as unknown[], pagesLoading: false, failedOwners: [] as string[], discoveryError: false, uisError: false, refetchPages: vi.fn(),
  importModule: vi.fn(), postJSON: vi.fn(), invoke: vi.fn(),
}));
vi.mock('../lib/pluginUIModule', () => ({ importPluginModule: mocks.importModule }));
vi.mock('../lib/pluginPages', async (original) => ({ ...await original<typeof import('../lib/pluginPages')>(),
  usePluginPages: () => ({ pages: mocks.pages, loading: mocks.pagesLoading, error: mocks.failedOwners.length > 0, failedOwners: mocks.failedOwners, discoveryError: mocks.discoveryError, refetch: mocks.refetchPages }) }));
vi.mock('../lib/api', async (original) => ({ ...await original<typeof import('../lib/api')>(),
  fetchJSON: () => mocks.uisError ? Promise.reject(new Error('offline')) : Promise.resolve(mocks.uis), postJSON: mocks.postJSON }));
vi.mock('../lib/plugins', async (original) => ({ ...await original<typeof import('../lib/plugins')>(),
  pluginActions: { invoke: mocks.invoke, list: () => Promise.resolve([]), artifactURL: () => '' } }));
vi.mock('./PluginPage', () => ({ PluginPage: ({ pageId }: { pageId?: string }) => <p>Declarative page {pageId || '(none)'}</p> }));

let ui: PluginUI;
const baseUI: PluginUI = {
  pluginId: 'org.example.catalog', pluginLabel: 'Catalog', ownerId: 'local', sdk: '^1.0.0', hostSdk: '1.0.0', compatible: true,
  index: { url: '/api/plugins/ui/bundle?pluginId=org.example.catalog&expose=index&sha256=abc', sha256: 'abc' },
  actions: [{ id: 'reset', label: 'Reset catalog', placement: 'global' }],
};

function Items() {
  const items = usePluginCall<{ name: string }[]>('items.list', { limit: 5 });
  const rename = usePluginMutation<{ ok: boolean }, { name: string }>('items.rename');
  const reset = usePluginAction('reset');
  return <>
    <ul>{items.data?.map((item) => <li key={item.name}>{item.name}</li>)}</ul>
    <button onClick={() => rename.mutate({ name: 'b' })}>Rename</button>
    <button disabled={reset.pending} onClick={() => void reset.run({ route: 'settings' })}>Reset</button>
    <Link to="items/7">Open item 7</Link>
  </>;
}

function Index() {
  const context = usePluginContext();
  return <>
    <p>Context {context.pluginId} {context.ownerId} {context.basePath}</p>
    <Routes><Route index element={<Items />} /><Route path="items/:id" element={<p>Item detail</p>} /></Routes>
  </>;
}

let test = 0;

beforeEach(() => {
  vi.clearAllMocks();
  // The host caches imports by URL, as the browser's module map does.
  test++;
  ui = { ...baseUI, index: { url: `${baseUI.index!.url}${test}`, sha256: `abc${test}` } };
  mocks.uis = [ui];
  mocks.pages = [];
  mocks.pagesLoading = false;
  mocks.failedOwners = [];
  mocks.discoveryError = false;
  mocks.uisError = false;
  mocks.importModule.mockResolvedValue(Index);
  mocks.postJSON.mockImplementation((_url: string, body: { method: string }) => Promise.resolve(body.method === 'items.list' ? [{ name: 'Alpha' }] : { ok: true }));
  mocks.invoke.mockResolvedValue({ results: [{ kind: 'notice', text: 'Catalog reset' }] });
});

function mount(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[path]}>
    <Routes><Route path="/plugins/:pluginId/*" element={<PluginRoute />} /></Routes>
  </MemoryRouter></QueryClientProvider>);
}

it('mounts the plugin index with scoped context, calls and nested relative routes', async () => {
  mount('/plugins/org.example.catalog');
  expect(await screen.findByText('Context org.example.catalog local /plugins/org.example.catalog')).toBeInTheDocument();
  expect(await screen.findByText('Alpha')).toBeInTheDocument();
  expect(mocks.importModule).toHaveBeenCalledWith(ui.index!.url);
  expect(mocks.postJSON).toHaveBeenCalledWith('/api/plugins/ui/call', { pluginId: ui.pluginId, method: 'items.list', params: { limit: 5 } }, expect.anything());
  fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
  await waitFor(() => expect(mocks.postJSON).toHaveBeenCalledWith('/api/plugins/ui/call', { pluginId: ui.pluginId, method: 'items.rename', params: { name: 'b' } }, expect.anything()));
  fireEvent.click(screen.getByRole('link', { name: 'Open item 7' }));
  expect(await screen.findByText('Item detail')).toBeInTheDocument();
});

it('runs plugin actions through the host action dialog', async () => {
  mount('/plugins/org.example.catalog/');
  fireEvent.click(await screen.findByRole('button', { name: 'Reset' }));
  expect(await screen.findByText('Catalog reset')).toBeInTheDocument();
  expect(mocks.invoke).toHaveBeenCalledWith(expect.objectContaining({ pluginId: ui.pluginId, ownerId: 'local', actionId: 'reset', placement: 'global',
    surface: 'command-palette', context: { ownerId: 'local', route: 'settings' } }), expect.anything());
  expect(screen.getByRole('button', { name: 'Reset' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Close' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Reset' })).toBeEnabled());
});

it('rejects undeclared actions', async () => {
  let failure: unknown;
  function Unknown() {
    const action = usePluginAction('missing');
    return <button onClick={() => action.run().catch((err: unknown) => { failure = err; })}>Run missing</button>;
  }
  mocks.importModule.mockResolvedValue(Unknown);
  mount('/plugins/org.example.catalog');
  fireEvent.click(await screen.findByRole('button', { name: 'Run missing' }));
  await waitFor(() => expect(String(failure)).toContain('Unknown plugin action: missing'));
});

it('lets declared page IDs keep their routes', async () => {
  mocks.pages = [{ pluginId: ui.pluginId, ownerId: 'local', page: { id: 'settings', label: 'Settings' } }];
  mount('/plugins/org.example.catalog/settings');
  expect(await screen.findByText('Declarative page settings')).toBeInTheDocument();
  expect(mocks.importModule).not.toHaveBeenCalled();
});

it('never loads UI for remote owners', async () => {
  mount('/plugins/org.example.catalog/catalog?ownerId=machine');
  expect(await screen.findByText('Declarative page catalog')).toBeInTheDocument();
  expect(mocks.importModule).not.toHaveBeenCalled();
});

it('explains incompatible SDK ranges instead of loading the bundle', async () => {
  mocks.uis = [{ ...ui, sdk: '^2.0.0', compatible: false, index: undefined }];
  mount('/plugins/org.example.catalog');
  expect(await screen.findByText(/built for @ocman\/plugin-ui \^2\.0\.0, but this ocman provides 1\.0\.0/)).toBeInTheDocument();
  expect(mocks.importModule).not.toHaveBeenCalled();
});

it('reports a compatible plugin without a mountable index', async () => {
  mocks.uis = [{ ...ui, index: undefined }];
  mount('/plugins/org.example.catalog');
  expect(await screen.findByText(/declares no index entry point/)).toBeInTheDocument();
});

it('contains failing bundles in the plugin boundary and retries the import', async () => {
  vi.spyOn(console, 'error').mockImplementation(() => {});
  mocks.importModule.mockRejectedValueOnce(new Error('network'));
  mount('/plugins/org.example.catalog');
  expect(await screen.findByText("This plugin's interface failed")).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
  expect(await screen.findByText('Alpha')).toBeInTheDocument();
  expect(mocks.importModule).toHaveBeenLastCalledWith(`${ui.index!.url}&attempt=1`);
});

it('falls back to declarative pages when the plugin has no UI', async () => {
  mocks.uis = [];
  mount('/plugins/org.example.other/home');
  expect(await screen.findByText('Declarative page home')).toBeInTheDocument();
});

it('keeps the host owner and refuses overlapping actions', async () => {
  const failures: string[] = [];
  function Overlap() {
    const first = usePluginAction('reset');
    const second = usePluginAction('reset');
    return <button onClick={() => {
      void first.run({ ownerId: 'machine', route: 'settings' });
      second.run().catch((err: unknown) => failures.push(String(err)));
    }}>Run twice</button>;
  }
  mocks.importModule.mockResolvedValue(Overlap);
  mount('/plugins/org.example.catalog');
  fireEvent.click(await screen.findByRole('button', { name: 'Run twice' }));
  expect(await screen.findByText('Catalog reset')).toBeInTheDocument();
  await waitFor(() => expect(failures).toEqual(['Error: Another plugin action is already open']));
  expect(mocks.invoke).toHaveBeenCalledOnce();
  expect(mocks.invoke.mock.calls[0][0].context).toEqual({ ownerId: 'local', route: 'settings' });
});

it('settles an open action when the plugin unmounts', async () => {
  let settled = false;
  function Pending() {
    const action = usePluginAction('reset');
    return <button onClick={() => void action.run().then(() => { settled = true; })}>Open</button>;
  }
  mocks.importModule.mockResolvedValue(Pending);
  mocks.invoke.mockReturnValue(new Promise(() => {}));
  const view = mount('/plugins/org.example.catalog');
  fireEvent.click(await screen.findByRole('button', { name: 'Open' }));
  view.unmount();
  await waitFor(() => expect(settled).toBe(true));
});

it('keeps a successful retry URL for later visits', async () => {
  vi.spyOn(console, 'error').mockImplementation(() => {});
  mocks.importModule.mockRejectedValueOnce(new Error('network'));
  const view = mount('/plugins/org.example.catalog');
  fireEvent.click(await screen.findByRole('button', { name: 'Try again' }));
  expect(await screen.findByText('Alpha')).toBeInTheDocument();
  view.unmount();
  mount('/plugins/org.example.catalog');
  expect(await screen.findByText('Alpha')).toBeInTheDocument();
  expect(mocks.importModule).toHaveBeenCalledTimes(2);
});

it('waits for declared pages before mounting UI on a sub-route', async () => {
  mocks.pagesLoading = true;
  mount('/plugins/org.example.catalog/items/7');
  expect(await screen.findByText('Loading plugin…')).toBeInTheDocument();
  expect(mocks.importModule).not.toHaveBeenCalled();
});

it('refuses to guess the tier when the local page catalog failed', async () => {
  mocks.failedOwners = ['local'];
  mount('/plugins/org.example.catalog/settings');
  expect(await screen.findByText('Could not load plugin pages')).toBeInTheDocument();
  expect(mocks.importModule).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(mocks.refetchPages).toHaveBeenCalledOnce();
});

it('still mounts local UI when only a remote page catalog failed', async () => {
  mocks.failedOwners = ['machine'];
  mount('/plugins/org.example.catalog/items/7');
  expect(await screen.findByText('Context org.example.catalog local /plugins/org.example.catalog')).toBeInTheDocument();
});

it('reports a failed interface catalog instead of an unavailable plugin', async () => {
  mocks.uisError = true;
  mount('/plugins/org.example.catalog');
  expect(await screen.findByText('Could not load plugin interfaces')).toBeInTheDocument();
  mocks.uisError = false;
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(await screen.findByText('Alpha')).toBeInTheDocument();
});

it('refuses to guess the tier when owner discovery failed', async () => {
  mocks.discoveryError = true;
  mount('/plugins/org.example.catalog/settings');
  expect(await screen.findByText('Could not load plugin pages')).toBeInTheDocument();
  expect(mocks.importModule).not.toHaveBeenCalled();
});
