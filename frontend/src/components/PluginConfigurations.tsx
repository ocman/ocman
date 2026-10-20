import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { callPluginPage, type PluginPage, type PageDocument } from '../lib/pluginPages';
import { shortPath } from '../lib/format';
import { Button } from './Control';

export type Configuration = { directory: string; projectId: string; document: PageDocument };

export function PluginConfigurations({ page, directories, onOpen }: {
  page: PluginPage; directories: string[]; onOpen: (configuration: Configuration) => void;
}) {
  const query = useQuery({
    queryKey: ['plugin-page-summary', page.ownerId, page.pluginId, page.page.id, directories],
    retry: false, gcTime: 0,
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const configurations: Configuration[] = [];
      let failed = 0;
      // Plugins may advertise a single call slot. Do not flood them with a
      // parallel read per project, which would hide configurations as busy.
      for (const directory of directories) {
        try {
          signal.throwIfAborted();
          const projectId = (await api.sessions({ dir: directory, limit: 0 }, signal))
            .find((s) => s.directory === directory && (s.remoteId || 'local') === page.ownerId)?.projectId;
          if (!projectId) throw new Error('Project identity unavailable');
          const document = await callPluginPage(page, projectId, 'read', undefined, signal);
          const configured = document.configured ?? (Object.values(document.values).some((v) => Array.isArray(v) ? v.length > 0 : Boolean(v)) || !!document.tree.available);
          if (configured) configurations.push({ directory, projectId, document });
        } catch {
          signal.throwIfAborted();
          failed++;
        }
      }
      return { configurations, failed };
    },
  });
  const configurations = query.data?.configurations ?? [];
  return <>
    {query.isPending && <p role="status">Loading configurations…</p>}
    {(query.isError || !!query.data?.failed) && <div><p role="alert">Some configurations could not be loaded. Check the owner's connection and project identities.</p><Button onClick={() => void query.refetch()}>Retry configurations</Button></div>}
    {!configurations.length && !query.isPending && !query.data?.failed && !query.isError && <p>No saved configurations. Create one to set up your targets.</p>}
    <div className="plugin-configuration-list">{configurations.map((configuration) => {
      const targets = configuration.document.values.targets;
      return <article key={configuration.directory} className="plugin-configuration-card">
        <div><h2>{shortPath(configuration.directory)}</h2><small>{page.ownerId} · {configuration.directory}</small></div>
        {Array.isArray(targets) && <div><p>{targets.length} {targets.length === 1 ? 'target' : 'targets'} · {targets.map((t) => String(t.name ?? '')).join(', ')}</p>
          {configuration.document.values.selected_target && <small>Target to scan: {String(configuration.document.values.selected_target)}</small>}</div>}
        <Button aria-label={`Open configuration for ${shortPath(configuration.directory)}`} onClick={() => onOpen(configuration)}>Open configuration</Button>
      </article>;
    })}</div>
  </>;
}
