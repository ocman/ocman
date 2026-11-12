import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { PluginRegistration } from '../lib/plugins';
import { useUnsavedChangesGuard } from '../lib/useUnsavedChangesGuard';
import { usePluginManagement } from '../lib/usePluginManagement';
import { Drawer } from './Drawer';
import { Button } from './Control';
import { ToggleField } from './ToggleField';
import { PluginNavIcon } from './PluginNavIcon';
import { PluginStatus } from './PluginCatalogList';
import { PluginSettingsContent } from './PluginSettingsCard';
import { Tabs, TabsList, TabsTrigger, TabsContent } from './Tabs';
import styles from './PluginCatalog.module.css';

export type PluginSettingsView = 'settings' | 'permissions' | 'diagnostics';
const views: PluginSettingsView[] = ['settings', 'permissions', 'diagnostics'];

export function PluginSettingsDrawer({ plugin, owner, ownerName, refresh }: {
  plugin: PluginRegistration; owner: string; ownerName: string; refresh: () => Promise<void>;
}) {
  const [params, setParams] = useSearchParams();
  const [dirty, setDirty] = useState(false);
  const requested = params.get('pluginTab') as PluginSettingsView;
  const view = views.includes(requested) ? requested : 'settings';
  const management = usePluginManagement(plugin, owner, refresh, () => {
    if (view !== 'settings') setParams((previous) => { const next = new URLSearchParams(previous); next.set('pluginTab', 'settings'); return next; });
  });
  const { busy, pendingAction } = management;
  const { confirmDiscard } = useUnsavedChangesGuard(dirty);
  async function navigate(tab?: string) {
    if (busy || (dirty && !await confirmDiscard())) return false;
    setDirty(false);
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (tab) next.set('pluginTab', tab);
      else { next.delete('plugin'); next.delete('pluginTab'); }
      return next;
    });
    return true;
  }
  return <Drawer title={plugin.description.name} icon={<PluginNavIcon ownerId={owner} pluginId={plugin.description.id} icon={plugin.description.icon} />}
    description={`${plugin.description.version} · Host: ${ownerName}`} onClose={() => void navigate()} canClose={!busy} closeLabel="Close plugin details">
    <div className={styles.detailSummary}>
      <PluginStatus plugin={plugin} />
      <div className={styles.quickActions}>
        <Button size="small" variant="ghost" onClick={() => void navigate('diagnostics')} disabled={busy}><i className="bi bi-terminal" aria-hidden="true" /> View logs</Button>
        <div className={styles.enableControl}><span>{pendingAction === 'disable' ? 'Disabling…' : pendingAction === 'enable' ? 'Enabling…' : 'Enabled'}</span>
          <ToggleField label={`Enable ${plugin.description.name}`} checked={plugin.enabled}
            disabled={busy || (!plugin.enabled && (plugin.removed || plugin.health.status === 'conflict'))}
            onChange={() => {
              if (plugin.enabled) void management.mutate('disable');
              else if (management.reviewEnable()) void navigate('permissions').then((ok) => { if (!ok) management.setReview(null); });
            }} />
        </div>
      </div>
    </div>
    <Tabs value={view} onValueChange={(tab) => void navigate(tab)}>
      <TabsList aria-label="Plugin details">
        {views.map((tab) => <TabsTrigger key={tab} value={tab} disabled={busy}>{tab[0].toUpperCase() + tab.slice(1)}</TabsTrigger>)}
      </TabsList>
      <TabsContent value={view}>
        <PluginSettingsContent plugin={plugin} owner={owner} management={management} view={view} onDirtyChange={setDirty} onCancel={() => void navigate()} onOpenSettings={() => void navigate('settings')} />
      </TabsContent>
    </Tabs>
    <p className={styles.muted}>Actions affect {plugin.description.name} on {ownerName} only.</p>
  </Drawer>;
}
