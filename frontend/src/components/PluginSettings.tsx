import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import type { HostCapabilityEntry } from '../lib/api.types';
import { plugins, type PluginRegistration } from '../lib/plugins';
import { SettingRow } from './SettingRow';
import { PluginSettingsCard } from './PluginSettingsCard';
import { Button, SelectField } from './Control';

export function PluginSettings() {
  const [hosts, setHosts] = useState<HostCapabilityEntry[] | null>(null);
  const [owner, setOwner] = useState('');
  const [error, setError] = useState(false);
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    api.capabilities(controller.signal).then((result) => {
      if (!controller.signal.aborted) { setHosts(result.hosts ?? []); setError(false); }
    }).catch(() => { if (!controller.signal.aborted) setError(true); });
    return () => controller.abort();
  }, [revision]);
  const selected = owner || hosts?.[0]?.remoteId || '';
  const host = hosts?.find((entry) => entry.remoteId === selected);
  return <>
    <SettingRow setting="plugin-owner">
      <SelectField aria-label="Plugin owner" value={selected} onChange={(event) => setOwner(event.target.value)}>
        {hosts?.map((entry) => <option key={entry.remoteId} value={entry.remoteId}>{entry.remoteName}</option>)}
        {owner && !host && <option value={owner}>{owner} (unavailable)</option>}
      </SelectField>
      <Button type="button" size="small" onClick={() => setRevision((n) => n + 1)}>Refresh owners</Button>
    </SettingRow>
    {error ? <p role="alert">Could not load plugin owners. Refresh owners to retry.</p>
      : !hosts ? <p role="status">Loading plugin owners…</p>
        : !host?.pluginManagement ? <p role="status">Plugin management is unavailable on this owner.</p>
          : <PluginCatalog key={selected} owner={selected} />}
  </>;
}

function PluginCatalog({ owner }: { owner: string }) {
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
    <SettingRow setting="plugin-discovery">
      <Button type="button" size="small" disabled={busy || (catalog === null && !error)} onClick={() => { void refresh(true); }}>Rescan plugins</Button>
      <Button type="button" size="small" disabled={busy || (catalog === null && !error)} onClick={() => { void refresh(); }}>Refresh health</Button>
    </SettingRow>
    {discovery.map((failure) => <p role="alert" key={failure.filename}>{failure.filename}: {failure.error}</p>)}
    {error ? <p role="alert">Could not load plugins for this owner. Refresh health to retry.</p>
      : catalog === null ? <p role="status">Loading plugins…</p>
        : catalog.length === 0 ? <p role="status">No plugins discovered.</p>
          : catalog.map((plugin) => <PluginSettingsCard key={plugin.description.id} plugin={plugin} owner={owner} refresh={() => refresh()} />)}
  </>;
}
