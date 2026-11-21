import type { ChangesSidebarTab } from '../lib/uiStore';
import type { PluginPane } from '../lib/pluginPanes';
import { PluginNavIcon } from './PluginNavIcon';
import { TAB_ICONS } from './rightPanelTabs';

// TabIcon picks the glyph for one right-panel tab, in the strip and in
// a pane header. A plugin-owned pane identifies itself with the
// plugin's own declared icon — the same descriptor navigation renders —
// while host tabs keep the fixed bootstrap-icon table.
export function TabIcon({ tab, pluginPane }: { tab: ChangesSidebarTab; pluginPane?: PluginPane }) {
  if (pluginPane) return <PluginNavIcon ownerId={pluginPane.ownerId} pluginId={pluginPane.pluginId} icon={pluginPane.icon} />;
  return <i className={`bi ${TAB_ICONS[tab] ?? 'bi-diagram-3'}`} aria-hidden="true" />;
}
