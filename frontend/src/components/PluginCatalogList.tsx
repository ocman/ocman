import { Link, useSearchParams } from 'react-router-dom';
import type { PluginRegistration } from '../lib/plugins';
import { Button, ButtonGroup, SearchField } from './Control';
import { DataTable } from './DataTable';
import { PageState } from './PageState';
import { PluginNavIcon } from './PluginNavIcon';
import styles from './PluginCatalog.module.css';

function pluginNeedsAttention(plugin: PluginRegistration) {
  return plugin.removed || ['conflict', 'unhealthy'].includes(plugin.health.status)
    || (plugin.enabled && !['ready', 'starting'].includes(plugin.health.status))
    || (plugin.enabled && plugin.description.requestedGrants?.some((grant) => !plugin.grants?.includes(grant))) === true;
}

export function PluginStatus({ plugin }: { plugin: PluginRegistration }) {
  const attention = pluginNeedsAttention(plugin);
  const label = plugin.removed ? 'Executable removed' : plugin.health.status === 'ready' ? 'Healthy' : plugin.health.status;
  return <span className={`${styles.status} ${attention ? styles.attention : plugin.enabled ? styles.healthy : styles.muted}`}>
    {plugin.enabled ? 'Enabled' : 'Disabled'}{(plugin.removed || plugin.health.status !== 'disabled') && ` · ${label}`}{attention && !plugin.removed && ' · Needs attention'}
  </span>;
}

const filters = [ ['all', 'All'], ['enabled', 'Enabled'], ['disabled', 'Disabled'], ['attention', 'Needs attention'] ] as const;

export function PluginCatalogList({ catalog, owner }: { catalog: PluginRegistration[]; owner: string }) {
  const [params, setParams] = useSearchParams();
  const query = params.get('pluginSearch') || '';
  const filter = params.get('pluginStatus') || 'all';
  const matches = (plugin: PluginRegistration, status: string) => status === 'enabled' ? plugin.enabled
    : status === 'disabled' ? !plugin.enabled : status === 'attention' ? pluginNeedsAttention(plugin) : true;
  const filtered = catalog.filter((plugin) => matches(plugin, filter)
    && `${plugin.description.name} ${plugin.description.id}`.toLowerCase().includes(query.trim().toLowerCase()))
    .sort((a, b) => a.description.name.localeCompare(b.description.name));
  const pages = Math.max(1, Math.ceil(filtered.length / 20));
  const requestedPage = Number(params.get('pluginPage'));
  const page = Number.isSafeInteger(requestedPage) ? Math.min(pages, Math.max(1, requestedPage)) : 1;
  const rows = filtered.slice((page - 1) * 20, page * 20);
  const update = (values: Record<string, string>, replace = false) => setParams((previous) => {
    const next = new URLSearchParams(previous);
    for (const [key, value] of Object.entries(values)) { if (value) next.set(key, value); else next.delete(key); }
    return next;
  }, { replace });
  if (!catalog.length) return <PageState icon="bi-plug" title="No plugins discovered." description="Install a plugin on this host, then rescan its plugin directory." />;
  return <>
    <SearchField aria-label="Search plugins" placeholder="Search plugins by name or ID…" value={query}
      onChange={(event) => update({ pluginSearch: event.target.value, pluginPage: '' }, true)} />
    <ButtonGroup label="Filter plugins by status">
      {filters.map(([value, label]) => <Button key={value} size="small" aria-pressed={filter === value} variant={filter === value ? 'accent' : 'ghost'}
        onClick={() => update({ pluginStatus: value === 'all' ? '' : value, pluginPage: '' })}>{label} {catalog.filter((plugin) => matches(plugin, value)).length}</Button>)}
    </ButtonGroup>
    {!filtered.length ? <PageState title="No matching plugins" description="Try another name or status.">
      <Button onClick={() => update({ pluginSearch: '', pluginStatus: '', pluginPage: '' })}>Clear filters</Button>
    </PageState> : <DataTable framed className={styles.table} aria-label="Plugins">
      <thead><tr><th>Plugin</th><th>Status</th><th>Version</th></tr></thead>
      <tbody>{rows.map((plugin) => {
        const next = new URLSearchParams(params);
        next.set('ownerId', owner); next.set('plugin', plugin.description.id); next.delete('pluginTab');
        const href = `?${next}`;
        return <tr key={plugin.description.id} onClick={(event) => {
          if ((event.target as HTMLElement).closest('a, button') || window.getSelection()?.toString()) return;
          update({ ownerId: owner, plugin: plugin.description.id, pluginTab: '' });
        }}>
          <td><div className={styles.pluginIdentity}><span className={styles.pluginIcon} aria-hidden="true"><PluginNavIcon ownerId={owner} pluginId={plugin.description.id} icon={plugin.description.icon} /></span>
            <div><Link to={href}>{plugin.description.name}</Link><small>{plugin.description.id}</small></div>
          </div></td>
          <td><PluginStatus plugin={plugin} /></td><td>{plugin.description.version}</td>
        </tr>;
      })}</tbody>
    </DataTable>}
    <div className={styles.toolbar}>
      <span className={styles.muted}>{filtered.length} of {catalog.length} plugins{pages > 1 && ` · Page ${page} of ${pages}`}</span>
      {pages > 1 && <ButtonGroup label="Plugin pages">
        <Button size="small" disabled={page === 1} onClick={() => update({ pluginPage: String(page - 1) })}>Previous</Button>
        <Button size="small" disabled={page === pages} onClick={() => update({ pluginPage: String(page + 1) })}>Next</Button>
      </ButtonGroup>}
    </div>
  </>;
}
