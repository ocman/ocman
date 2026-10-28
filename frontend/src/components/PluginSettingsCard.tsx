import { useState } from 'react';
import { hasConversationCapability, plugins, type PluginInput, type PluginMutation, type PluginRegistration } from '../lib/plugins';
import { SettingRow } from './SettingRow';
import { SettingsSection } from './SettingsSection';
import { PluginConfiguration } from './PluginConfiguration';
import { PluginDeliveryBacklog } from './PluginDeliveryBacklog';
import { Button, ButtonGroup } from './Control';
import styles from './PluginSettingsCard.module.css';

const UI_GRANT = 'ui.app';

function PluginMetadata({ plugin: p }: { plugin: PluginRegistration }) {
  const d = p.description;
  return <>
    <SettingRow label="Immutable ID"><code>{d.id}</code></SettingRow>
    <SettingRow label="Version">{d.version}</SettingRow>
    <SettingRow label="Checksum" block><code className={styles.checksum}>{p.checksum}</code></SettingRow>
    <SettingRow label="Capabilities">{d.capabilities?.map((c) => `${c.name} v${c.version.major}.${c.version.minor}`).join(', ') || 'None'}</SettingRow>
    <SettingRow label="Execution scope">{d.scope}</SettingRow>
    {d.ui && <SettingRow label="Interface">In-app UI · @ocman/plugin-ui {d.ui.sdk}</SettingRow>}
    <SettingRow label="Requested grants">{d.requestedGrants?.join(', ') || 'None'}</SettingRow>
    <SettingRow label="Current grants">{p.grants?.join(', ') || 'None'}</SettingRow>
    <SettingRow label="Status">{p.enabled ? 'Enabled' : 'Disabled'} · {p.health.status}{p.removed ? ' · Executable removed' : ''}</SettingRow>
    <SettingRow label="Restart count">{p.health.restartCount}</SettingRow>
    <SettingRow label="Last error" block>{p.health.lastError || 'None'}</SettingRow>
    {d.settings?.filter((s) => s.secret).map((s) => <SettingRow key={s.key} label={`${s.label} secret`}>
      {p.configuration.secrets?.[s.key] ? 'Configured' : 'Not configured'}
    </SettingRow>)}
  </>;
}

/** In-app plugin UI is code running inside ocman, not data ocman renders. */
function PluginUIDisclosure({ plugin: p, owner }: { plugin: PluginRegistration; owner: string }) {
  if (!p.description.ui) return null;
  const index = p.description.ui.exposes.index;
  return <div role="note" aria-label="Plugin interface disclosure">
    <p><strong>This plugin runs UI code with your ocman session.</strong> Approving <code>{UI_GRANT}</code> loads its interface into ocman, where it can do anything you can do in ocman.</p>
    {index && <p>Approved interface bundle SHA-256: <code className={styles.checksum}>{index.sha256}</code></p>}
    {owner !== 'local' && <p>Plugins on remote owners keep their declarative views. Their interface is not loaded in this version.</p>}
  </div>;
}

export function PluginSettingsCard({ plugin: p, owner, refresh }: {
  plugin: PluginRegistration; owner: string; refresh: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [review, setReview] = useState<'enable' | 'grants' | null>(null);
  const [configure, setConfigure] = useState(false);
  const [remove, setRemove] = useState(false);
  const [logs, setLogs] = useState<string | null>(null);
  const d = p.description;
  const blocked = p.removed || p.health.status === 'conflict';

  async function mutate(action: PluginMutation, input?: PluginInput) {
    setBusy(true);
    setError('');
    try {
      await plugins.mutate(owner, d.id, action, input);
      setReview(null); setConfigure(false); setRemove(false); setLogs(null);
    } catch {
      // Never echo an error body that could contain submitted configuration.
      setError(action === 'configuration'
        ? 'Configuration was not activated. The server attempts to restore the last working configuration; check the current values and health below.'
        : `Could not ${action === 'grants' ? 'update grants for' : action} plugin. Check its health and retry.`);
      if (action === 'configuration') setConfigure(false);
    } finally {
      await refresh();
      setBusy(false);
    }
  }

  async function loadLogs() {
    setBusy(true); setError('');
    try {
      const result = await plugins.stderr(owner, d.id);
      setLogs(result.stderr.slice(-128 * 1024));
    } catch { setError('Could not load recent stderr for this owner.'); }
    finally { setBusy(false); }
  }

  return <SettingsSection title={`${d.name} plugin`}>
    <PluginMetadata plugin={p} />
    {error && <p role="alert">{error}</p>}
    <fieldset disabled={busy} className={styles.management}>
      <legend>Manage {d.name}</legend>
      <SettingRow label="Lifecycle" block>
        <ButtonGroup label="Plugin lifecycle actions">
          <Button type="button" size="small" variant="accent" disabled={p.enabled || blocked} onClick={() => setReview('enable')}>Enable</Button>
          <Button type="button" size="small" disabled={blocked || !d.requestedGrants?.some((grant) => !p.grants?.includes(grant))} onClick={() => setReview('grants')}>Grant permissions</Button>
          <Button type="button" size="small" disabled={!p.enabled} onClick={() => { void mutate('disable'); }}>Disable</Button>
          <Button type="button" size="small" disabled={!p.enabled || blocked} onClick={() => { void mutate('retry'); }}>Retry</Button>
          <Button type="button" size="small" disabled={!p.enabled || blocked} onClick={() => { void mutate('restart'); }}>Restart</Button>
          <Button type="button" size="small" onClick={() => setConfigure(!configure)}>Configure</Button>
          <Button type="button" size="small" variant="danger" disabled={!p.grants?.length} onClick={() => { void mutate('grants', { grants: [] }); }}>Revoke grants</Button>
          <Button type="button" size="small" variant="danger" disabled={p.enabled} onClick={() => setRemove(true)}>Remove data</Button>
        </ButtonGroup>
      </SettingRow>
      {review && <SettingRow label={review === 'enable' ? 'Review grants before enabling' : 'Review grants'} block>
        <p>This native executable runs on {owner} with {d.scope} scope. Approve checksum <code>{p.checksum}</code> and these requested grants:</p>
        <ul>{(d.requestedGrants ?? []).map((grant) => <li key={grant}>{grant}</li>)}</ul>
        {!d.requestedGrants?.length && <p>No grants requested.</p>}
        <PluginUIDisclosure plugin={p} owner={owner} />
        <ButtonGroup label="Plugin approval actions">
          <Button type="button" variant="accent" onClick={() => { void mutate(review, { ...(review === 'enable' ? { approval: p.approval } : {}), grants: d.requestedGrants ?? [] }); }}>{review === 'enable' ? 'Approve grants and enable' : 'Approve grants'}</Button>
          <Button type="button" onClick={() => setReview(null)}>Cancel approval</Button>
        </ButtonGroup>
      </SettingRow>}
      {configure && <PluginConfiguration key={JSON.stringify(p.configuration)} plugin={p} save={(input) => mutate('configuration', input)} />}
      {remove && <SettingRow label="Permanently remove plugin data" block>
        <p>Delete configuration, secrets, grants, and private data for {d.id} on {owner}. This cannot be undone. The executable is not deleted.</p>
        <ButtonGroup label="Plugin removal actions">
          <Button type="button" variant="danger" onClick={() => { void mutate('remove-data'); }}>Confirm permanent removal</Button>
          <Button type="button" onClick={() => setRemove(false)}>Cancel removal</Button>
        </ButtonGroup>
      </SettingRow>}
      {hasConversationCapability(p) && <PluginDeliveryBacklog plugin={p} owner={owner} />}
      <SettingRow label="Recent stderr" desc="Bounded diagnostic output from the selected owner." block>
        <Button type="button" size="small" onClick={() => { void loadLogs(); }}>Load recent stderr</Button>
        {logs !== null && <pre aria-label="Recent stderr" className={styles.logs}>{logs || 'No recent stderr.'}</pre>}
      </SettingRow>
    </fieldset>
  </SettingsSection>;
}
