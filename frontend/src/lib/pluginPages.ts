import { useQueries } from '@tanstack/react-query';
import { fetchJSON, postJSON } from './api';
import { useHostCapabilities } from './settingsQueries';
import type { PluginPaneTree } from './pluginPanes';

export type PluginPage = { ownerId: string; pluginId: string; page: { id: string; label: string } };
export type PageField = { key: string; label: string; type: 'text' | 'multiline' | 'secret' | 'boolean' | 'list' | 'projects' | 'select'; help?: string; fields?: PageField[]; optionsFrom?: string };
export type PageValues = { [key: string]: string | boolean | PageValues[] };
export type PageDocument = { revision: string; fields: PageField[] | null; values: PageValues; tree: PluginPaneTree; configured?: boolean };

// Only a declared text field may name a row outside its editable controls.
// Undeclared values and other field types may contain secrets or opaque JSON.
export function pageRowName(fields: PageField[] | undefined, row: unknown): string {
  const first = fields?.[0];
  if (first?.type !== 'text' || !row || typeof row !== 'object' || Array.isArray(row)) return '';
  const value = (row as Record<string, unknown>)[first.key];
  return typeof value === 'string' ? value : '';
}

export function blankPageValues(fields: PageField[]): PageValues {
  return Object.fromEntries(fields.map((f) => [f.key, f.type === 'list' ? [] : f.type === 'boolean' ? false : '']));
}

export function pageURL(page: PluginPage) {
  return `/plugins/${encodeURIComponent(page.pluginId)}/${encodeURIComponent(page.page.id)}?${new URLSearchParams({ ownerId: page.ownerId })}`;
}

export function usePluginPages() {
  const hosts = useHostCapabilities();
  const queries = useQueries({ queries: (hosts.data ?? []).filter((host) => host.pluginManagement).map((host) => ({
    queryKey: ['plugin-pages', host.remoteId],
    queryFn: ({ signal }: { signal: AbortSignal }) => fetchJSON<PluginPage[]>(`/api/plugins/pages?${new URLSearchParams({ ownerId: host.remoteId })}`, signal),
    retry: false,
    refetchInterval: 15_000,
  })) });
  return { pages: queries.flatMap((q) => q.isError ? [] : q.data ?? []), loading: hosts.isPending || queries.some((q) => q.isPending), error: hosts.isError || queries.some((q) => q.isError) };
}

export function callPluginPage(page: PluginPage, projectId: string, method: 'read' | 'save', document?: PageDocument, signal?: AbortSignal) {
  return postJSON<PageDocument>('/api/plugins/pages/call', {
    ownerId: page.ownerId, pluginId: page.pluginId, method,
    request: { pageId: page.page.id, projectId, ...(method === 'save' && document ? { revision: document.revision, values: document.values } : {}) },
  }, { signal });
}
