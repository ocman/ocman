import { Link, useLocation } from 'react-router-dom';
import { pageURL, usePluginPages, type PluginPage } from '../lib/pluginPages';
import { usePluginUIs } from '../lib/pluginUI';

type NavEntry = { ownerId: string; pluginId: string; name: string; to: string };

function pluginLabel(page: PluginPage) {
  return page.pluginLabel || page.page.label;
}

/** One entry per plugin and owner. Hub-local in-app UI opens its index. */
function usePluginNavEntries(): NavEntry[] {
  const { pages } = usePluginPages();
  const { uis } = usePluginUIs();
  // Only a loadable interface replaces the plugin's declarative entry.
  const entries: NavEntry[] = uis.filter((ui) => ui.compatible && ui.index).map((ui) => ({ ownerId: ui.ownerId, pluginId: ui.pluginId, name: ui.pluginLabel, to: `/plugins/${encodeURIComponent(ui.pluginId)}` }));
  for (const page of pages) {
    if (!entries.some((e) => e.ownerId === page.ownerId && e.pluginId === page.pluginId)) {
      entries.push({ ownerId: page.ownerId, pluginId: page.pluginId, name: pluginLabel(page), to: pageURL(page) });
    }
  }
  return entries;
}

export function PluginPageTitle() {
  const { pages } = usePluginPages();
  const { uis } = usePluginUIs();
  const location = useLocation();
  const owner = new URLSearchParams(location.search).get('ownerId') || 'local';
  const page = pages.find((p) => p.ownerId === owner && pageURL(p).split('?')[0] === location.pathname);
  // In-app UI owns every sub-route of its plugin; declarative pages do not.
  const ui = uis.find((u) => u.ownerId === owner && `${location.pathname}/`.startsWith(`/plugins/${encodeURIComponent(u.pluginId)}/`));
  return <>{page ? pluginLabel(page) : ui?.pluginLabel ?? 'Plugin workspace'}</>;
}

export function PluginPageNav({ onNavigate, replace }: { onNavigate?: () => void; replace?: boolean }) {
  const entries = usePluginNavEntries();
  const location = useLocation();
  const owner = new URLSearchParams(location.search).get('ownerId') || 'local';
  return <>{entries.map((entry) => {
    const duplicate = entries.filter((e) => e.name === entry.name).length > 1;
    const label = duplicate ? `${entry.name} (${entry.ownerId})` : entry.name;
    const active = owner === entry.ownerId && `${location.pathname}/`.startsWith(`/plugins/${encodeURIComponent(entry.pluginId)}/`);
    return <Link key={`${entry.ownerId}/${entry.pluginId}`} to={entry.to} replace={replace} aria-label={label} title={`${entry.name} on ${entry.ownerId}`}
      className={active ? 'active' : undefined}
      aria-current={active ? 'page' : false} onClick={onNavigate}>
      <i className="bi bi-puzzle" aria-hidden="true" /><span>{label}</span>
    </Link>;
  })}</>;
}
