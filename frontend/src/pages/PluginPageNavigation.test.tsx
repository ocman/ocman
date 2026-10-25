// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { PluginPage } from './PluginPage';

const mocks = vi.hoisted(() => ({ call: vi.fn(), actions: vi.fn() }));
const page = { pluginId: 'org.example.pentest', ownerId: 'machine', page: { id: 'pentest', label: 'Pentest', scope: 'plugin' } };
vi.mock('../lib/pluginPages', async (original) => ({ ...await original<typeof import('../lib/pluginPages')>(),
  usePluginPages: () => ({ pages: [page], loading: false }), callPluginPage: mocks.call,
}));
vi.mock('../lib/queries', () => ({ useProjects: () => ({ data: [] }) }));
vi.mock('../lib/plugins', async (original) => ({ ...await original<typeof import('../lib/plugins')>(), pluginActions: { list: mocks.actions } }));

beforeEach(() => {
  mocks.actions.mockResolvedValue([]);
  mocks.call.mockReset();
  mocks.call.mockImplementation((_page, _project, _method, _draft, _signal, context) => Promise.resolve({
    revision: 'r1', fields: [{ key: 'name', label: 'Name', type: 'text' }],
    values: { name: context?.itemId ? 'Item value' : 'Homepage value' }, tree: {},
    ...(!context?.itemId && { collection: { title: 'Items', columns: [{ key: 'name', label: 'Name', display: 'text' }],
      rows: [{ id: 'item-a', values: { name: 'Item A' } }], nextCursor: context?.cursor ? undefined : 'next' } }),
  }));
});
afterEach(() => vi.restoreAllMocks());

function mount(itemId = '') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={[
    `/plugins/org.example.pentest/pentest?ownerId=machine${itemId ? `&itemId=${itemId}` : ''}`,
  ]}><Routes><Route path="/plugins/:pluginId/:pageId" element={<PluginPage />} /></Routes></MemoryRouter></QueryClientProvider>);
}

it('discards an item draft before displaying or saving the homepage', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  mount('item-a');
  fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Discard this item draft' } });
  fireEvent.click(screen.getByRole('button', { name: 'Back to list' }));
  await waitFor(() => expect(screen.getByLabelText('Name')).toHaveValue('Homepage value'));
  expect(window.confirm).toHaveBeenCalledWith('Discard unsaved edits?');
  fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }));
  await screen.findByText('Saved.');
  const save = mocks.call.mock.calls.find((call) => call[2] === 'save');
  expect(save?.[3].values).toEqual({ name: 'Homepage value' });
  expect(save?.[5]).toEqual({});
});

it('does not carry the list cursor into an item or back to the homepage', async () => {
  mount();
  fireEvent.click(await screen.findByRole('button', { name: 'Next page' }));
  await screen.findByRole('button', { name: 'First page' });
  fireEvent.click(screen.getByRole('link', { name: 'View item-a' }));
  await waitFor(() => expect(screen.getByLabelText('Name')).toHaveValue('Item value'));
  expect(mocks.call).toHaveBeenLastCalledWith(page, '', 'read', undefined, expect.any(AbortSignal), { itemId: 'item-a' });
  fireEvent.click(screen.getByRole('button', { name: 'Back to list' }));
  await screen.findByRole('button', { name: 'Next page' });
  expect(screen.queryByRole('button', { name: 'First page' })).not.toBeInTheDocument();
});
