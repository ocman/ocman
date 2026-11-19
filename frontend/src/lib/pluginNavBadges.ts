import { useQueries } from '@tanstack/react-query';
import { fetchJSON } from './api';

export type PluginNavBadge = { kind: 'dot' } | { kind: 'count'; count: number };
type OwnerBadges = Record<string, PluginNavBadge>;

export function usePluginNavBadges(owners: string[]) {
  const queries = useQueries({ queries: owners.map((owner) => ({
    queryKey: ['plugin-nav-badges', owner],
    queryFn: ({ signal }: { signal: AbortSignal }) => fetchJSON<OwnerBadges>(
      `/api/plugins/nav-badges?${new URLSearchParams({ ownerId: owner })}`,
      AbortSignal.any([signal, AbortSignal.timeout(4_000)]),
    ),
    staleTime: 0,
    refetchInterval: 3_000,
    networkMode: 'always' as const,
    retry: false,
  })) });
  // Do not show a cached live badge for an owner that has stopped responding.
  return new Map(owners.map((owner, i) => [owner, queries[i].isError ? undefined : queries[i].data]));
}
