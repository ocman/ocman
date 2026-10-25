import { NavLink, useLocation } from 'react-router-dom';
import { pageURL, usePluginPages } from '../lib/pluginPages';

export function PluginPageTitle() {
  const { pages } = usePluginPages();
  const location = useLocation();
  const owner = new URLSearchParams(location.search).get('ownerId') || 'local';
  const page = pages.find((p) => p.ownerId === owner && pageURL(p).split('?')[0] === location.pathname);
  return <>{page?.page.label ?? 'Plugin workspace'}</>;
}

export function PluginPageNav({ onNavigate, replace }: { onNavigate?: () => void; replace?: boolean }) {
  const { pages } = usePluginPages();
  const location = useLocation();
  const owner = new URLSearchParams(location.search).get('ownerId') || 'local';
  return <>{pages.map((page) => {
    const duplicate = pages.filter((p) => p.page.label === page.page.label).length > 1;
    const label = duplicate ? `${page.page.label} (${page.ownerId})` : page.page.label;
    return <NavLink key={pageURL(page)} to={pageURL(page)} replace={replace} aria-label={label} title={`${page.page.label} on ${page.ownerId}`}
      className={({ isActive }) => isActive && owner === page.ownerId ? 'active' : undefined}
      aria-current={owner === page.ownerId ? undefined : false} onClick={onNavigate}>
      <i className="bi bi-puzzle" aria-hidden="true" /><span>{label}</span>
    </NavLink>;
  })}</>;
}
