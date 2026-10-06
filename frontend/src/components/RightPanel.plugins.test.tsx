// @vitest-environment jsdom
import { QueryClient, QueryClientProvider, focusManager } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { RightPanel } from './RightPanel';
import { useUiStore } from '../lib/uiStore';
import type { Session } from '../lib/api';
import { pluginPaneTab } from '../lib/pluginPanes';

vi.mock('../lib/useUpstreams', () => ({ useUpstreams: () => ({ upstreams: [] }) }));

const pane = { pluginId: 'org.example.tree', ownerId: 'local', pane: { id: 'items', label: 'Tree' } };
const tree = { available: true, nodes: [{ id: 'item-1', title: 'Item', status: 'open', badge: 'P1' }] };
const props = {
  sessionId: 's1', platformId: 'opencode', directory: '/repo', session: { remoteId: '' } as Session,
  messageBookmarkGroups: [], selectedMessageBookmarkKey: null,
  onRemoveMessageBookmark: vi.fn(), onScrollToMessageBookmark: vi.fn(),
};

beforeEach(() => {
  useUiStore.persist.setOptions({ storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} } });
  useUiStore.setState({ changesSidebarOpenTabs: [], changesSidebarTabOrder: [], changesSidebarTabSizes: {} });
});
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); focusManager.setFocused(undefined); });

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (owner = '') => <QueryClientProvider client={client}><RightPanel {...props} session={{ remoteId: owner } as Session} /></QueryClientProvider>;
  return { ...render(view()), client, view };
}

it('makes no data requests while closed, including project switches and focus', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify([pane])));
  const { rerender, view } = renderPanel();
  expect(await screen.findByRole('tab', { name: 'Tree' })).toHaveAttribute('aria-selected', 'false');
  rerender(view('machine'));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  await act(async () => { focusManager.setFocused(false); focusManager.setFocused(true); });
  expect(fetch.mock.calls.every(([url]) => !String(url).includes('/panes/read'))).toBe(true);
});

it('reads only after opening, refreshes, and aborts a read when closed', async () => {
  let reads = 0;
  let pendingSignal: AbortSignal | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, options) => {
    if (!String(url).includes('/panes/read')) return new Response(JSON.stringify([pane]));
    if (++reads > 1) {
      pendingSignal = options?.signal as AbortSignal;
      return new Promise<Response>(() => {});
    }
    return new Response(JSON.stringify(tree));
  });
  renderPanel();
  const tab = await screen.findByRole('tab', { name: 'Tree' });
  expect(reads).toBe(0);
  await userEvent.click(tab);
  expect(await screen.findByText('item-1')).toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }));
  await waitFor(() => expect(reads).toBe(2));
  await userEvent.click(screen.getByRole('tab', { name: 'Tree' }));
  await waitFor(() => expect(pendingSignal?.aborted).toBe(true));
  expect(screen.queryByText('item-1')).not.toBeInTheDocument();
  expect(screen.getByLabelText('Changes (collapsed)')).toBeInTheDocument();
});

it('removes disabled plugin panes and cancels their requests', async () => {
  useUiStore.setState({ changesSidebarOpenTabs: [pluginPaneTab(pane)] });
  let signal: AbortSignal | undefined;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, options) => {
    if (!String(url).includes('/panes/read')) return new Response(JSON.stringify([pane]));
    signal = options?.signal as AbortSignal;
    return new Promise<Response>(() => {});
  });
  const { client } = renderPanel();
  await waitFor(() => expect(signal).toBeDefined());
  act(() => client.setQueryData(['plugin-panes', 'local'], []));
  await waitFor(() => expect(screen.queryByRole('tab', { name: 'Tree' })).not.toBeInTheDocument());
  await waitFor(() => expect(signal?.aborted).toBe(true));
});

it('shows an unavailable workspace and supports retry after an initial error', async () => {
  useUiStore.setState({ changesSidebarOpenTabs: [pluginPaneTab(pane)] });
  let fail = true;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
    if (!String(url).includes('/panes/read')) return new Response(JSON.stringify([pane]));
    if (fail) throw new Error('offline');
    return new Response('{"available":false}');
  });
  renderPanel();
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load Tree');
  fail = false;
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(await screen.findByText('This pane is unavailable for this project.')).toBeInTheDocument();
});

it('offers catalog retry when the panel was collapsed', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch')
    .mockRejectedValueOnce(new Error('catalog offline'))
    .mockResolvedValue(new Response(JSON.stringify([pane])));
  renderPanel();
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load plugin panes');
  expect(screen.queryByRole('tab', { name: 'Tree' })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: 'Retry plugin panes' }));
  expect(await screen.findByRole('tab', { name: 'Tree' })).toHaveAttribute('aria-selected', 'false');
  expect(fetch.mock.calls.every(([url]) => !String(url).includes('/panes/read'))).toBe(true);
});

it('fails closed on a catalog refresh error and restores the open pane on retry', async () => {
  useUiStore.setState({ changesSidebarOpenTabs: [pluginPaneTab(pane)] });
  let failCatalog = false;
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
    if (String(url).includes('/panes/read')) return new Response(JSON.stringify(tree));
    if (failCatalog) throw new Error('catalog offline');
    return new Response(JSON.stringify([pane]));
  });
  const { client } = renderPanel();
  expect(await screen.findByText('item-1')).toBeInTheDocument();
  failCatalog = true;
  act(() => { void client.invalidateQueries({ queryKey: ['plugin-panes', 'local'] }); });
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load plugin panes');
  expect(screen.queryByRole('tab', { name: 'Tree' })).not.toBeInTheDocument();
  expect(screen.queryByText('item-1')).not.toBeInTheDocument();
  failCatalog = false;
  await userEvent.click(screen.getByRole('button', { name: 'Retry plugin panes' }));
  expect(await screen.findByText('item-1')).toBeInTheDocument();
});
