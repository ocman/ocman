import { useQueries } from '@tanstack/react-query';
import { fetchJSON } from './api';
import { useHostCapabilities } from './settingsQueries';
import type { PluginAction } from './plugins';

export type PluginNoticeTone = 'info' | 'warning' | 'critical';

/** A declared action the host resolved for a notice; the label is the declaration's. */
export type PluginNoticeAction = { actionId: string; label: string; placement: PluginAction['action']['placement'] };

export type PluginNotice = {
  id: string;
  tone: PluginNoticeTone;
  title: string;
  body?: string;
  /** Unix milliseconds; the toast shows the elapsed time since then. */
  startedAt?: number;
  actions: PluginNoticeAction[];
};

type OwnerNotices = Record<string, { pluginLabel: string; notices: PluginNotice[] }>;

export type ListedPluginNotice = { key: string; ownerId: string; pluginId: string; pluginLabel: string; notice: PluginNotice };

export const PLUGIN_NOTICE_POLL_MS = 3_000;

/** Stable per owner, plugin and notice ID, so a new ID shows again after a dismissal. */
export function pluginNoticeKey(ownerId: string, pluginId: string, noticeId: string) {
  return JSON.stringify([ownerId, pluginId, noticeId]);
}

/**
 * Polls every plugin-managing owner for its active notices. An owner that
 * fails to answer contributes none, so a stale toast never outlives its owner.
 */
export function usePluginNotices() {
  const hosts = useHostCapabilities();
  const owners = (hosts.data ?? []).filter((host) => host.pluginManagement).map((host) => host.remoteId);
  const queries = useQueries({ queries: owners.map((owner) => ({
    queryKey: ['plugin-notices', owner],
    queryFn: ({ signal }: { signal: AbortSignal }) => fetchJSON<OwnerNotices>(
      `/api/plugins/notices?${new URLSearchParams({ ownerId: owner })}`,
      AbortSignal.any([signal, AbortSignal.timeout(4_000)]),
    ),
    staleTime: 0,
    refetchInterval: PLUGIN_NOTICE_POLL_MS,
    networkMode: 'always' as const,
    retry: false,
  })) });
  const notices: ListedPluginNotice[] = [];
  owners.forEach((ownerId, i) => {
    const query = queries[i];
    if (query.isError || !query.data || typeof query.data !== 'object') return;
    for (const [pluginId, entry] of Object.entries(query.data)) {
      for (const notice of Array.isArray(entry?.notices) ? entry.notices : []) {
        notices.push({ key: pluginNoticeKey(ownerId, pluginId, notice.id), ownerId, pluginId, pluginLabel: entry.pluginLabel, notice });
      }
    }
  });
  return { notices };
}
