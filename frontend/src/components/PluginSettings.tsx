import { useEffect, useState, type ReactNode } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useHostCapabilities } from '../lib/settingsQueries';
import { usePluginSettingsLocation } from '../lib/usePluginSettingsLocation';
import { plugins, type PluginRegistration } from '../lib/plugins';
import { SettingsSection } from './SettingsSection';
import { SettingRow } from './SettingRow';
import { PluginCatalogList } from './PluginCatalogList';
import { PluginSettingsDrawer } from './PluginSettingsDrawer';
import { PageState } from './PageState';
import { Button, ButtonGroup, SelectField } from './Control';
import styles from './PluginCatalog.module.css';

export function PluginSettings() {
  const hostsQ = useHostCapabilities();
  const hosts = hostsQ.data ?? null;
  const error = hostsQ.isError;
  const [, setParams] = useSearchParams();
  const { params, dirty, setDirty } = usePluginSettingsLocation();
  const owner = params.get('ownerId') || '';
  const selected = owner || hosts?.[0]?.remoteId || '';
  const host = hosts?.find((entry) => entry.remoteId === selected);
  const ownerControls = <SettingRow setting="plugin-owner" label="Host" compact>
    <ButtonGroup label="Plugin owner controls">
      <SelectField aria-label="Plugin owner" value={selected} onChange={(event) => setParams((previous) => {
        const next = new URLSearchParams(previous);
        next.set('ownerId', event.target.value);
        next.delete('plugin'); next.delete('pluginTab');
        return next;
      })}>
        {hosts?.map((entry) => <option key={entry.remoteId} value={entry.remoteId}>{entry.remoteName}</option>)}
        {owner && !host && <option value={owner}>{owner} (unavailable)</option>}
      </SelectField>
      <Button type="button" onClick={() => { void hostsQ.refetch(); }}>Refresh owners</Button>
    </ButtonGroup>
  </SettingRow>;
  if (!error && host?.pluginManagement) {
    return <PluginCatalog key={selected} owner={selected} ownerName={host.remoteName} ownerControls={ownerControls} params={params} dirty={dirty} onDirtyChange={setDirty} />;
  }
  return <SettingsSection title="Discovery">
    {ownerControls}
    {error ? <p role="alert">Could not load plugin owners. Refresh owners to retry.</p>
      : !hosts ? <p role="status">Loading plugin owners…</p>
        : <p role="status">Plugin management is unavailable on this owner.</p>}
  </SettingsSection>;
}

function PluginCatalog({ owner, ownerName, ownerControls, params, dirty, onDirtyChange }: {
  owner: string; ownerName: string; ownerControls: ReactNode; params: URLSearchParams; dirty: boolean; onDirtyChange: (dirty: boolean) => void;
}) {
  const [, setParams] = useSearchParams();
  const [catalog, setCatalog] = useState<PluginRegistration[] | null>(null);
  const [discovery, setDiscovery] = useState<{ filename: string; error: string }[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    Promise.all([plugins.list(owner, controller.signal), plugins.discovery(owner, controller.signal)]).then(([result, failures]) => {
      if (!controller.signal.aborted) { setCatalog(result ?? []); setDiscovery(failures); }
    }).catch(() => { if (!controller.signal.aborted) setError(true); });
    return () => controller.abort();
  }, [owner]);

  async function refresh(rescan = false) {
    setBusy(true);
    try {
      setCatalog((await (rescan ? plugins.rescan(owner) : plugins.list(owner))) ?? []);
      setDiscovery(await plugins.discovery(owner));
      setError(false);
    } catch {
      setError(true);
    } finally { setBusy(false); }
  }

  const selected = catalog?.find((plugin) => plugin.description.id === params.get('plugin'));
  return <div className={styles.catalog}>
    <div className={styles.toolbar} aria-label="Plugin management">
      {ownerControls}
      <SettingRow setting="plugin-discovery" label="Discovery" compact>
        <ButtonGroup label="Plugin discovery actions">
          <Button type="button" disabled={busy || (catalog === null && !error)} onClick={() => { void refresh(true); }}>Rescan plugins</Button>
          <Button type="button" disabled={busy || (catalog === null && !error)} onClick={() => { void refresh(); }}>Refresh health</Button>
        </ButtonGroup>
      </SettingRow>
    </div>
    {discovery.length > 0 && <details className={styles.warning}>
      <summary>{discovery.length} plugin discovery {discovery.length === 1 ? 'issue' : 'issues'}</summary>
      {discovery.map((failure) => <p role="alert" key={failure.filename}>{failure.filename}: {failure.error}</p>)}
    </details>}
    {error && catalog === null ? <PageState error title="Plugins are unavailable" description="Could not load plugins for this owner. Refresh health to retry." onRetry={() => void refresh()} retrying={busy} />
      : <>
        {error && <p role="alert">Could not load plugins for this owner. Refresh health to retry.</p>}
        {catalog === null ? <div role="status" aria-label="Loading plugins" className={styles.skeleton}>Loading plugins…
          {[0, 1, 2, 3, 4].map((row) => <div key={row} className={styles.skeletonRow} aria-hidden="true" />)}
        </div>
          : <PluginCatalogList catalog={catalog} owner={owner} />}
      </>}
    {selected && <PluginSettingsDrawer key={selected.description.id} plugin={selected} owner={owner} ownerName={ownerName} refresh={() => refresh()} tab={params.get('pluginTab')} dirty={dirty} onDirtyChange={onDirtyChange} />}
    {catalog && params.get('plugin') && !selected && <PageState compact title="Plugin is no longer available" description="It may have been removed from this host.">
      <Button onClick={() => setParams((previous) => { const next = new URLSearchParams(previous); next.delete('plugin'); next.delete('pluginTab'); return next; })}>Dismiss</Button>
    </PageState>}
  </div>;
}
