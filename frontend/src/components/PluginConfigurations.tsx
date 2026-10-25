import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { withDeadline } from '../lib/coalescedRefresh';
import { callPluginPage, pageRowName, type PluginPage, type PageDocument } from '../lib/pluginPages';
import { shortPath } from '../lib/format';
import { Button } from './Control';

export type Configuration = { directory: string; projectId: string; document: PageDocument };
export const CONFIGURATION_TIMEOUT_MS = 35_000;

export function PluginConfigurations({ page, directories, onOpen }: {
  page: PluginPage; directories: string[]; onOpen: (configuration: Configuration) => void;
}) {
  const client = useQueryClient();
  const queryKey = ['plugin-page-summary', page.ownerId, page.pluginId, page.page.id, directories];
  const query = useQuery({
    queryKey,
    retry: false, gcTime: 0,
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const configurations: Configuration[] = [];
      let failed = 0;
      let completed = 0;
      // Plugins may advertise a single call slot. Do not flood them with a
      // parallel read per project, which would hide configurations as busy.
      for (const directory of directories) {
        try {
          signal.throwIfAborted();
          const { projectId, document } = await withDeadline(CONFIGURATION_TIMEOUT_MS, async (deadline) => {
            const projectId = (await api.sessions({ dir: directory, limit: 0 }, deadline))
              .find((s) => s.directory === directory && (s.remoteId || 'local') === page.ownerId)?.projectId;
            if (!projectId) throw new Error('Project identity unavailable');
            const document = await callPluginPage(page, projectId, 'read', undefined, deadline);
            return { projectId, document };
          }, signal);
          signal.throwIfAborted();
          const configured = document.configured ?? (Object.values(document.values).some((v) => Array.isArray(v) ? v.length > 0 : Boolean(v)) || !!document.tree.available);
          if (configured) configurations.push({ directory, projectId, document });
        } catch {
          signal.throwIfAborted();
          failed++;
        }
        completed++;
        // Publish each completed check rather than hiding all results behind
        // the slowest project. Copy the array before it enters the query cache.
        client.setQueryData(queryKey, { configurations: [...configurations], failed, completed });
      }
      return { configurations, failed, completed };
    },
  });
  const configurations = query.data?.configurations ?? [];
  return <>
    {query.isFetching && <p role="status">Loading configurations… {query.data?.completed ?? 0}/{directories.length} projects checked.</p>}
    {(query.isError || !!query.data?.failed) && <div><p role="alert">Some configurations could not be loaded. Check the owner's connection and project identities.</p><Button disabled={query.isFetching} onClick={() => void query.refetch()}>Retry configurations</Button></div>}
    {!configurations.length && !query.isPending && !query.isFetching && !query.data?.failed && !query.isError && <p>No saved configurations. Create one to set up your targets.</p>}
    <div className="plugin-configuration-list">{configurations.map((configuration) => {
      const targets = configuration.document.values.targets;
      const schema = configuration.document.fields?.find((field) => field.key === 'targets' && field.type === 'list');
      const names = Array.isArray(targets) ? targets.map((row) => pageRowName(schema?.fields, row)).filter(Boolean) : [];
      const selection = configuration.document.fields?.find((field) => field.key === 'selected_target' && field.type === 'select' && field.optionsFrom === 'targets');
      const selected = selection && configuration.document.values[selection.key];
      return <article key={configuration.directory} className="plugin-configuration-card">
        <div><h2>{shortPath(configuration.directory)}</h2><small>{page.ownerId} · {configuration.directory}</small></div>
        {names.length > 0 && <div><p>{names.length} {names.length === 1 ? 'target' : 'targets'} · {names.join(', ')}</p>
          {typeof selected === 'string' && names.includes(selected) && <small>Target to scan: {selected}</small>}</div>}
        <Button aria-label={`Open configuration for ${shortPath(configuration.directory)}`} onClick={() => onOpen(configuration)}>Open configuration</Button>
      </article>;
    })}</div>
  </>;
}
