import { useEffect, useState, type ReactNode } from 'react';
import { useHostCapabilities } from '../lib/settingsQueries';
import { plugins, type PluginRegistration } from '../lib/plugins';
import { SettingRow } from './SettingRow';
import { SettingsSection } from './SettingsSection';
import { PluginSettingsCard } from './PluginSettingsCard';
import { Button, ButtonGroup, SelectField } from './Control';

export function PluginSettings() {
  const hostsQ = useHostCapabilities();
  const hosts = hostsQ.data ?? null;
  const error = hostsQ.isError;
  const [owner, setOwner] = useState('');
  const selected = owner || hosts?.[0]?.remoteId || '';
  const host = hosts?.find((entry) => entry.remoteId === selected);
  const ownerControls = <SettingRow setting="plugin-owner" block>
    <ButtonGroup label="Plugin owner controls">
      <SelectField aria-label="Plugin owner" value={selected} onChange={(event) => setOwner(event.target.value)}>
        {hosts?.map((entry) => <option key={entry.remoteId} value={entry.remoteId}>{entry.remoteName}</option>)}
        {owner && !host && <option value={owner}>{owner} (unavailable)</option>}
      </SelectField>
      <Button type="button" onClick={() => { void hostsQ.refetch(); }}>Refresh owners</Button>
    </ButtonGroup>
  </SettingRow>;
  if (!error && host?.pluginManagement) {
    return <PluginCatalog key={selected} owner={selected} ownerControls={ownerControls} />;
  }
  return <SettingsSection title="Discovery">
    {ownerControls}
    {error ? <p role="alert">Could not load plugin owners. Refresh owners to retry.</p>
      : !hosts ? <p role="status">Loading plugin owners…</p>
        : <p role="status">Plugin management is unavailable on this owner.</p>}
  </SettingsSection>;
}

function PluginCatalog({ owner, ownerControls }: { owner: string; ownerControls: ReactNode }) {
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
      setCatalog(null);
    } finally { setBusy(false); }
  }

  return <>
    <SettingsSection title="Discovery">
      {ownerControls}
      <SettingRow setting="plugin-discovery" block>
        <ButtonGroup label="Plugin discovery actions">
          <Button type="button" disabled={busy || (catalog === null && !error)} onClick={() => { void refresh(true); }}>Rescan plugins</Button>
          <Button type="button" disabled={busy || (catalog === null && !error)} onClick={() => { void refresh(); }}>Refresh health</Button>
        </ButtonGroup>
      </SettingRow>
      {discovery.map((failure) => <p role="alert" key={failure.filename}>{failure.filename}: {failure.error}</p>)}
      {error ? <p role="alert">Could not load plugins for this owner. Refresh health to retry.</p>
        : catalog === null ? <p role="status">Loading plugins…</p>
          : catalog.length === 0 ? <p role="status">No plugins discovered.</p>
            : null}
    </SettingsSection>
    {!error && catalog?.map((plugin) => <PluginSettingsCard key={plugin.description.id} plugin={plugin} owner={owner} refresh={() => refresh()} />)}
  </>;
}
