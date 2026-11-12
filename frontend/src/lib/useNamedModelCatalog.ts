import { useQueries } from '@tanstack/react-query';
import { api, type SessionModelEntry } from './api';
import { basePlatform } from './modelRef';
import { useMachinePlatforms } from './useCapabilities';
import { queryClient } from './queryClient';

/** Load each account separately. Prepared catalogs deliberately exclude sibling accounts. */
export function useNamedModelCatalog(directory: string | string[], owner: string, fallback: SessionModelEntry[]) {
  const platforms = useMachinePlatforms(owner);
  const named = platforms.some((platform) => !!platform.kind);
  const available = platforms.filter((platform) => platform.kind && platform.available && platform.capabilities.modelCatalog);
  const directories = [...new Set((Array.isArray(directory) ? directory : [directory]).filter(Boolean))];
  const targets = directories.flatMap((directory) => available.map((platform) => ({ directory, platform })));
  const catalogs = useQueries({ queries: targets.map(({ directory, platform }) => ({
    queryKey: ['named-model-catalog', owner, directory, platform.id],
    queryFn: ({ signal }: { signal: AbortSignal }) => api.prepareSession({ directory, remoteId: owner, platform: platform.id, selectedOnly: true }, signal),
    staleTime: 60_000,
    retry: false,
  })) }, queryClient);
  const entries = named ? catalogs.flatMap((catalog, index) => (catalog.data?.models.models ?? []).filter((model) =>
    !model.hidden && (!catalog.data?.models.hasProviders || model.isAvailable !== false)).map((model) => ({
    ...model, platform: basePlatform(targets[index].platform.id), platformName: targets[index].platform.displayName,
  }))) : fallback;
  return { named, entries, loading: catalogs.some((catalog) => catalog.isPending), error: catalogs.some((catalog) => catalog.isError), refresh: () => { for (const catalog of catalogs) void catalog.refetch(); } };
}
