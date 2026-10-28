import { useQueries } from '@tanstack/react-query';
import { fetchJSON, postJSON } from './api';
import { useHostCapabilities } from './settingsQueries';
import type { PluginPaneTree } from './pluginPanes';

export type PluginPage = { ownerId: string; pluginId: string; pluginLabel?: string; page: { id: string; label: string; validate?: boolean; scope?: 'project' | 'plugin' } };
export type PageField = { key: string; label: string; type: 'text' | 'multiline' | 'secret' | 'boolean' | 'list' | 'projects' | 'model' | 'select' | 'number' | 'integer'; help?: string; fields?: PageField[]; optionsFrom?: string;
  required?: boolean; minimum?: number; maximum?: number; minLength?: number; maxLength?: number; minItems?: number; maxItems?: number; options?: { value: string; label: string }[] };
export type PageValues = { [key: string]: string | boolean | number | PageValues[] };
export type PageIssue = { path: string; message: string };
export type PageDisplay = 'text' | 'number' | 'boolean' | 'status' | 'summary' | 'severity';
export type PageSummary = { label: string; description?: string; count?: number };
export type PageValue = string | number | boolean | PageSummary;
export type PageCollection = { title: string; detailPresentation?: 'drawer'; columns: { key: string; label: string; display: PageDisplay }[]; rows: { id: string; projectId?: string; values: Record<string, PageValue> }[]; nextCursor?: string };
export type PageDetail = { title: string; properties: { label: string; display: PageDisplay; value: PageValue }[] };
export type PageDocument = { revision: string; fields: PageField[] | null; values: PageValues; tree: PluginPaneTree; configured?: boolean; collection?: PageCollection; detail?: PageDetail; issues?: PageIssue[] };

// Only a declared text field may name a row outside its editable controls.
// Undeclared values and other field types may contain secrets or opaque JSON.
export function pageRowName(fields: PageField[] | undefined, row: unknown): string {
  const first = fields?.[0];
  if (first?.type !== 'text' || !row || typeof row !== 'object' || Array.isArray(row)) return '';
  const value = (row as Record<string, unknown>)[first.key];
  return typeof value === 'string' ? value : '';
}

export function blankPageValues(fields: PageField[]): PageValues {
  return Object.fromEntries(fields.filter((f) => f.type !== 'number' && f.type !== 'integer').map((f) => [f.key, f.type === 'list' ? [] : f.type === 'boolean' ? false : '']));
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
  const owners = (hosts.data ?? []).filter((host) => host.pluginManagement).map((host) => host.remoteId);
  return { pages: queries.flatMap((q) => q.isError ? [] : q.data ?? []), loading: hosts.isPending || queries.some((q) => q.isPending), error: hosts.isError || queries.some((q) => q.isError),
    /** Owners whose page catalog failed, so their declarations are unknown rather than empty. */
    failedOwners: owners.filter((_, i) => queries[i]?.isError),
    refetch: () => Promise.all([hosts.refetch(), ...queries.map((q) => q.refetch())]) };
}

export function callPluginPage(page: PluginPage, projectId: string, method: 'read' | 'save', document?: PageDocument, signal?: AbortSignal, context: { itemId?: string; cursor?: string } = {}) {
  return postJSON<PageDocument>('/api/plugins/pages/call', {
    ownerId: page.ownerId, pluginId: page.pluginId, method,
    request: { pageId: page.page.id, projectId, ...context, ...(method === 'save' && document ? { revision: document.revision, values: document.values } : {}) },
  }, { signal });
}
