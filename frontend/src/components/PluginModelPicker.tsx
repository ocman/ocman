import { useQueries } from '@tanstack/react-query';
import { fetchJSON, type SessionModelsResponse } from '../lib/api';
import { useCapabilities } from '../lib/useCapabilities';
import { basePlatform } from '../lib/modelRef';
import { withDeadline } from '../lib/coalescedRefresh';
import { Button } from './Control';
import { ModelSelect } from './ModelSelect';

export function PluginModelPicker({ ownerId, label, value, onChange }: {
  ownerId: string; label: string; value: string; onChange: (value: string) => void;
}) {
  const capabilities = useCapabilities();
  const platforms = (capabilities?.platforms ?? []).filter((p) =>
    (p.remoteId || 'local') === ownerId && p.available && p.capabilities.modelCatalog);
  const queries = useQueries({ queries: platforms.map((platform) => ({
    queryKey: ['plugin-model-catalog', ownerId, platform.id], retry: false, staleTime: 60_000,
    queryFn: ({ signal }: { signal: AbortSignal }) => withDeadline(15_000, (deadline) =>
      fetchJSON<SessionModelsResponse>(`/api/plugins/model-catalog?${new URLSearchParams({ ownerId, platform: basePlatform(platform.id) })}`, deadline), signal),
  })) });
  const entries = queries.flatMap((query, index) => query.isError ? [] : (query.data?.models ?? []).map((model) => ({
    ...model, platform: basePlatform(platforms[index].id), platformName: platforms[index].displayName,
  })));
  const loading = !capabilities || queries.some((query) => query.isPending);
  const failed = queries.some((query) => query.isError);
  return <>
    <ModelSelect ariaLabel={label} value={value} onChange={onChange} models={[]} modelEntries={entries} defaultLabel="Use default model" />
    {loading && <small role="status">Loading models…</small>}
    {failed && <div><small role="alert">Some model catalogs could not be loaded. Check the owner's connection.</small>
      <Button onClick={() => { for (const query of queries) void query.refetch(); }}>Retry models</Button></div>}
    {!loading && !entries.length && !failed && <small role="status">No models available on this owner. The default or saved selection is retained.</small>}
  </>;
}
