import { Link, useLocation } from 'react-router-dom';
import { pageURL, usePluginPages, type PluginPage } from '../lib/pluginPages';

function pluginLabel(page: PluginPage) {
  return page.pluginLabel || page.page.label;
}

export function PluginPageTitle() {
  const { pages } = usePluginPages();
  const location = useLocation();
  const owner = new URLSearchParams(location.search).get('ownerId') || 'local';
  const page = pages.find((p) => p.ownerId === owner && pageURL(p).split('?')[0] === location.pathname);
  return <>{page ? pluginLabel(page) : 'Plugin workspace'}</>;
}

export function PluginPageNav({ onNavigate, replace }: { onNavigate?: () => void; replace?: boolean }) {
  const { pages } = usePluginPages();
  const location = useLocation();
  const owner = new URLSearchParams(location.search).get('ownerId') || 'local';
  const plugins = pages.filter((page, i) => pages.findIndex((p) => p.ownerId === page.ownerId && p.pluginId === page.pluginId) === i);
  return <>{plugins.map((page) => {
    const name = pluginLabel(page);
    const duplicate = plugins.filter((p) => pluginLabel(p) === name).length > 1;
    const label = duplicate ? `${name} (${page.ownerId})` : name;
    const active = owner === page.ownerId && location.pathname.startsWith(`/plugins/${encodeURIComponent(page.pluginId)}/`);
    return <Link key={pageURL(page)} to={pageURL(page)} replace={replace} aria-label={label} title={`${name} on ${page.ownerId}`}
      className={active ? 'active' : undefined}
      aria-current={active ? 'page' : false} onClick={onNavigate}>
      <i className="bi bi-puzzle" aria-hidden="true" /><span>{label}</span>
    </Link>;
  })}</>;
}
