import { Link, useLocation } from 'react-router-dom';
import { pageURL, usePluginPages, type PluginPage } from '../lib/pluginPages';
import { pluginHeaderTitle, usePluginTitles, usePluginUIs } from '../lib/pluginUI';
import type { PluginIconDescriptor } from '../lib/pluginIcons';
import { PluginNavIcon } from './PluginNavIcon';
import { usePluginNavBadges, type PluginNavBadge } from '../lib/pluginNavBadges';
import './PluginPageNav.css';

type NavEntry = { ownerId: string; pluginId: string; name: string; to: string; icon?: PluginIconDescriptor; navBadge?: boolean };

function pluginLabel(page: PluginPage) {
  return page.pluginLabel || page.page.label;
}

/** One entry per plugin and owner. Hub-local in-app UI opens its index. */
function usePluginNavEntries(): NavEntry[] {
  const { pages } = usePluginPages();
  const { uis } = usePluginUIs();
  // Only a loadable interface replaces the plugin's declarative entry.
  const entries: NavEntry[] = uis.filter((ui) => ui.compatible && ui.index).map((ui) => ({ ownerId: ui.ownerId, pluginId: ui.pluginId, name: ui.pluginLabel, icon: ui.icon, navBadge: ui.navBadge, to: `/plugins/${encodeURIComponent(ui.pluginId)}` }));
  for (const page of pages) {
    if (!entries.some((e) => e.ownerId === page.ownerId && e.pluginId === page.pluginId)) {
      entries.push({ ownerId: page.ownerId, pluginId: page.pluginId, name: pluginLabel(page), icon: page.icon, navBadge: page.navBadge, to: pageURL(page) });
    }
  }
  return entries;
}

export function PluginPageTitle() {
  const { pages } = usePluginPages();
  const { uis } = usePluginUIs();
  const titles = usePluginTitles((state) => state.titles);
  const location = useLocation();
  const owner = new URLSearchParams(location.search).get('ownerId') || 'local';
  const page = pages.find((p) => p.ownerId === owner && pageURL(p).split('?')[0] === location.pathname);
  // In-app UI owns every sub-route of its plugin; declarative pages do not.
  const ui = uis.find((u) => u.ownerId === owner && `${location.pathname}/`.startsWith(`/plugins/${encodeURIComponent(u.pluginId)}/`));
  return <>{page ? pluginLabel(page) : ui ? pluginHeaderTitle(ui.pluginLabel, titles[ui.pluginId]) : 'Plugin workspace'}</>;
}

type NavProps = { onNavigate?: () => void; replace?: boolean };
type NavLinksProps = NavProps & { entries: NavEntry[] };

export function PluginPageNav(props: NavProps) {
  const entries = usePluginNavEntries();
  const owners = [...new Set(entries.filter((entry) => entry.navBadge).map((entry) => entry.ownerId))];
  return owners.length ? <PluginNavLinksWithBadges {...props} entries={entries} owners={owners} /> : <PluginNavLinks {...props} entries={entries} />;
}

function PluginNavLinksWithBadges({ owners, ...props }: NavLinksProps & { owners: string[] }) {
  return <PluginNavLinks {...props} badges={usePluginNavBadges(owners)} />;
}

function PluginNavLinks({ entries, badges, onNavigate, replace }: NavLinksProps & { badges?: ReturnType<typeof usePluginNavBadges> }) {
  const location = useLocation();
  const owner = new URLSearchParams(location.search).get('ownerId') || 'local';
  return <>{entries.length > 0 && <hr className="main-nav-plugin-separator" />}{entries.map((entry) => {
    const duplicate = entries.filter((e) => e.name === entry.name).length > 1;
    const label = duplicate ? `${entry.name} (${entry.ownerId})` : entry.name;
    const active = owner === entry.ownerId && `${location.pathname}/`.startsWith(`/plugins/${encodeURIComponent(entry.pluginId)}/`);
    const badge = entry.navBadge ? badges?.get(entry.ownerId)?.[entry.pluginId] : undefined;
    const status = badgeLabel(badge);
    return <Link key={`${entry.ownerId}/${entry.pluginId}`} to={entry.to} replace={replace}
      aria-label={status ? `${label}, ${status}` : label}
      title={`${entry.name} on ${entry.ownerId}${status ? `, ${status}` : ''}`}
      className={active ? 'active' : undefined}
      aria-current={active ? 'page' : false} onClick={onNavigate}>
      <PluginNavIcon ownerId={entry.ownerId} pluginId={entry.pluginId} icon={entry.icon} /><span>{label}</span>
      {badge?.kind === 'dot' && <b className="nav-unread-badge plugin-nav-badge plugin-nav-badge--dot" aria-hidden="true" />}
      {badge?.kind === 'count' && <b className="nav-unread-badge plugin-nav-badge" aria-hidden="true">{badge.count > 99 ? '99+' : badge.count}</b>}
    </Link>;
  })}</>;
}

function badgeLabel(badge?: PluginNavBadge) {
  if (badge?.kind === 'dot') return 'notification indicator';
  if (badge?.kind === 'count') return `${badge.count} ${badge.count === 1 ? 'notification' : 'notifications'}`;
  return '';
}
