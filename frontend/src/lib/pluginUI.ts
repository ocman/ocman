import { useQuery } from '@tanstack/react-query';
import { fetchJSON, postJSON } from './api';
import type { PluginAction } from './plugins';

/** A hub-local plugin that ships in-app UI (ui.v1), listed without running it. */
export interface PluginUI {
  pluginId: string;
  pluginLabel: string;
  ownerId: string;
  /** The @ocman/plugin-ui range the bundle was built against. */
  sdk: string;
  hostSdk: string;
  compatible: boolean;
  index?: { url: string; sha256: string };
  actions: PluginAction['action'][];
}

export function usePluginUIs() {
  const query = useQuery({
    queryKey: ['plugin-uis'],
    queryFn: ({ signal }) => fetchJSON<PluginUI[]>('/api/plugins/ui', signal),
    retry: false,
    refetchInterval: 15_000,
  });
  return { uis: query.data ?? [], loading: query.isPending, error: query.isError, refetch: () => query.refetch() };
}

export function callPluginUI<T>(pluginId: string, method: string, params: unknown, signal?: AbortSignal) {
  return postJSON<T>('/api/plugins/ui/call', { pluginId, method, ...(params === undefined ? {} : { params }) }, { signal });
}

/** The root query key of every ui.v1 call made by one plugin. */
export const pluginUIQueryKey = (pluginId: string) => ['plugin-ui-call', pluginId];
