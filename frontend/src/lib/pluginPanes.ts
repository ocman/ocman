import { fetchJSON } from './api';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { ChangesSidebarTab } from './uiStoreConfig';
import { useDocumentVisible } from './usePanelVisible';

export type PluginPane = {
  pluginId: string;
  ownerId: string;
  pane: { id: string; label: string };
};
export type PluginTreeNode = {
  id: string;
  title: string;
  parentId?: string;
  status?: string;
  badge?: string;
  kind?: string;
};
export type PluginPaneTree = { available: boolean; nodes?: PluginTreeNode[]; warning?: boolean };

export function pluginPaneTab(pane: PluginPane): ChangesSidebarTab {
  return `plugin:${pane.pluginId}/${pane.pane.id}`;
}

export function usePluginPanes(ownerId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: ['plugin-panes', ownerId],
    enabled: !!ownerId && enabled,
    retry: false,
    queryFn: ({ signal }) => fetchJSON<PluginPane[]>(`/api/plugins/panes?${new URLSearchParams({ ownerId: ownerId! })}`, signal),
  });
}

export function usePluginPaneTree(pane: PluginPane, directory: string | undefined) {
  const visible = useDocumentVisible();
  const client = useQueryClient();
  const queryKey = ['plugin-pane-tree', pane.ownerId, pane.pluginId, pane.pane.id, directory] as const;
  return useQuery<PluginPaneTree>({
    queryKey,
    enabled: !!directory && visible,
    retry: false,
    refetchInterval: (query) => query.state.data?.available ? 30_000 : false,
    refetchIntervalInBackground: false,
    queryFn: async ({ signal }) => {
      const params = new URLSearchParams({ ownerId: pane.ownerId, pluginId: pane.pluginId, paneId: pane.pane.id, directory: directory! });
      const next = await fetchJSON<PluginPaneTree>(`/api/plugins/panes/read?${params}`, signal);
      const previous = client.getQueryData<PluginPaneTree>(queryKey);
      return previous?.available && next.available && next.warning
        ? { ...next, nodes: previous.nodes ?? next.nodes }
        : next;
    },
  });
}
