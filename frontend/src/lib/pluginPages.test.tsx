// @vitest-environment jsdom
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { beforeEach, expect, it, vi } from 'vitest';
import { blankPageValues, callPluginPage, pageRowName, usePluginPages, type PageDocument, type PageField } from './pluginPages';
import { fetchJSON, postJSON } from './api';

const hosts = vi.hoisted(() => ({ data: [] as { remoteId: string; pluginManagement: boolean }[], isPending: false, isError: false, refetch: vi.fn() }));
vi.mock('./settingsQueries', () => ({ useHostCapabilities: () => hosts }));
vi.mock('./api', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));

beforeEach(() => {
  vi.clearAllMocks(); hosts.isPending = false; hosts.isError = false;
  hosts.data = [{ remoteId: 'local', pluginManagement: true }, { remoteId: 'remote', pluginManagement: true }, { remoteId: 'legacy', pluginManagement: false }];
});

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>;
}

it.each(['secret', 'multiline', 'boolean', 'list', 'projects', 'select'] as const)('never uses a %s field as a row name', (type) => {
  expect(pageRowName([{ key: 'value', label: 'Value', type }], { value: 'private' })).toBe('');
});

it.each([null, false, 42, 'opaque', [], {}, { title: 42 }].map((row) => ({ row })))('ignores malformed row names in $row', ({ row }) => {
  expect(pageRowName([{ key: 'title', label: 'Title', type: 'text' }], row)).toBe('');
});

it('uses only the declared first text field and ignores undeclared values', () => {
  const fields: PageField[] = [{ key: 'title', label: 'Title', type: 'text' }];
  expect(pageRowName(fields, { title: 'production', password: 'secret' })).toBe('production');
  expect(pageRowName(undefined, { title: 'secret' })).toBe('');
  expect(pageRowName([], { title: 'secret' })).toBe('');
});

it('lists pages independently on capable owners without calling legacy owners', async () => {
  vi.mocked(fetchJSON).mockImplementation(async (url) => [{ ownerId: url.includes('local') ? 'local' : 'remote', pluginId: 'plugin', page: { id: 'pentest', label: 'Pentest' } }] as never);
  const { result } = renderHook(() => usePluginPages(), { wrapper });
  await waitFor(() => expect(result.current.pages).toHaveLength(2));
  expect(result.current.loading).toBe(false);
  expect(result.current.error).toBe(false);
  expect(fetchJSON).toHaveBeenCalledTimes(2);
  expect(fetchJSON).toHaveBeenCalledWith('/api/plugins/pages?ownerId=remote', expect.any(AbortSignal));
});

it('drops stale owner declarations on errors instead of falling back', async () => {
  vi.mocked(fetchJSON).mockRejectedValue(new Error('offline'));
  const { result } = renderHook(() => usePluginPages(), { wrapper });
  await waitFor(() => expect(result.current.error).toBe(true));
  expect(result.current.pages).toEqual([]);
});

it('handles owner discovery loading and failure without issuing calls', () => {
  hosts.data = []; hosts.isPending = true;
  const { result, rerender } = renderHook(() => usePluginPages(), { wrapper });
  expect(result.current.loading).toBe(true);
  hosts.isPending = false; hosts.isError = true; rerender();
  expect(result.current.error).toBe(true);
  expect(fetchJSON).not.toHaveBeenCalled();
});

it('sends explicit owner/project identity and revisions only when saving', async () => {
  const page = { ownerId: 'remote', pluginId: 'plugin', page: { id: 'pentest', label: 'Pentest' } };
  const signal = new AbortController().signal;
  const document: PageDocument = { revision: 'r1', fields: [], values: { name: 'staging' }, tree: { available: false } };
  await callPluginPage(page, 'project-a', 'read', document, signal);
  expect(postJSON).toHaveBeenLastCalledWith('/api/plugins/pages/call', {
    ownerId: 'remote', pluginId: 'plugin', method: 'read', request: { pageId: 'pentest', projectId: 'project-a' },
  }, { signal });
  await callPluginPage(page, 'project-a', 'save', document);
  expect(postJSON).toHaveBeenLastCalledWith('/api/plugins/pages/call', {
    ownerId: 'remote', pluginId: 'plugin', method: 'save', request: { pageId: 'pentest', projectId: 'project-a', revision: 'r1', values: { name: 'staging' } },
  }, { signal: undefined });
});

it('keeps new numeric values absent and forwards explicit item and cursor identities', async () => {
  expect(blankPageValues([
    { key: 'name', label: 'Name', type: 'text' }, { key: 'count', label: 'Count', type: 'integer' },
    { key: 'price', label: 'Price', type: 'number' }, { key: 'enabled', label: 'Enabled', type: 'boolean' },
    { key: 'rows', label: 'Rows', type: 'list', fields: [] },
  ])).toEqual({ name: '', enabled: false, rows: [] });
  const page = { ownerId: 'remote', pluginId: 'plugin', page: { id: 'jobs', label: 'Jobs' } };
  await callPluginPage(page, 'project-a', 'read', undefined, undefined, { itemId: 'item-a', cursor: 'next' });
  expect(postJSON).toHaveBeenLastCalledWith('/api/plugins/pages/call', {
    ownerId: 'remote', pluginId: 'plugin', method: 'read', request: { pageId: 'jobs', projectId: 'project-a', itemId: 'item-a', cursor: 'next' },
  }, { signal: undefined });
});

it('reports which owners failed and refetches every catalog', async () => {
  vi.mocked(fetchJSON).mockImplementation((url: string) => url.includes('remote') ? Promise.reject(new Error('offline')) : Promise.resolve([]));
  const { result } = renderHook(() => usePluginPages(), { wrapper });
  await waitFor(() => expect(result.current.failedOwners).toEqual(['remote']));
  const calls = vi.mocked(fetchJSON).mock.calls.length;
  await result.current.refetch();
  expect(hosts.refetch).toHaveBeenCalled();
  expect(vi.mocked(fetchJSON).mock.calls.length).toBe(calls + 2);
});
