import { useParams, useSearchParams } from 'react-router-dom';
import { usePluginPages } from '../lib/pluginPages';
import { usePluginUIs } from '../lib/pluginUI';
import { ErrorState } from '../components/ErrorState';
import { LoadingState } from '../components/LoadingState';
import { PluginUIHost } from '../components/PluginUIHost';
import { PluginPage } from './PluginPage';

/**
 * Owns /plugins/:pluginId/*. A first segment naming a declared page renders
 * that page.v1 workspace. Otherwise a hub-local plugin with in-app UI gets
 * the whole subtree, which its Index routes with nested relative <Routes>.
 */
export function PluginRoute() {
  const { pluginId = '', '*': rest = '' } = useParams();
  const [search] = useSearchParams();
  const ownerId = search.get('ownerId') || 'local';
  const segment = rest.split('/')[0];
  const { pages, loading: pagesLoading } = usePluginPages();
  const { uis, loading: uisLoading } = usePluginUIs();
  if (pages.some((p) => p.ownerId === ownerId && p.pluginId === pluginId && p.page.id === segment)) return <PluginPage pageId={segment} />;
  const ui = ownerId === 'local' ? uis.find((u) => u.pluginId === pluginId) : undefined;
  // Never run a bundle on a URL that may still turn out to be a declared page.
  if (ownerId === 'local' && (uisLoading || (pagesLoading && !!segment))) return <LoadingState>Loading plugin…</LoadingState>;
  if (!ui) return <PluginPage pageId={segment} />;
  if (!ui.compatible) return <ErrorState title="Plugin interface not loaded"
    description={`${ui.pluginLabel} was built for @ocman/plugin-ui ${ui.sdk}, but this ocman provides ${ui.hostSdk}. Update the plugin or ocman.`} />;
  if (!ui.index) return <ErrorState title="Plugin interface not loaded" description={`${ui.pluginLabel} declares no index entry point that this ocman can mount.`} />;
  return <PluginUIHost key={ui.index.sha256} ui={{ ...ui, index: ui.index }} />;
}
