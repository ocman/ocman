import { useState } from 'react';
import { plugins, type PluginInput, type PluginMutation, type PluginRegistration } from '../lib/plugins';
import { SettingRow } from './SettingRow';
import { PluginConfiguration } from './PluginConfiguration';

function PluginMetadata({ plugin: p }: { plugin: PluginRegistration }) {
  const d = p.description;
  return <>
    <SettingRow label="Immutable ID"><code>{d.id}</code></SettingRow>
    <SettingRow label="Version">{d.version}</SettingRow>
    <SettingRow label="Checksum" block><code style={{ overflowWrap: 'anywhere' }}>{p.checksum}</code></SettingRow>
    <SettingRow label="Capabilities">{d.capabilities?.map((c) => `${c.name} v${c.version.major}.${c.version.minor}`).join(', ') || 'None'}</SettingRow>
    <SettingRow label="Execution scope">{d.scope}</SettingRow>
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

export function PluginSettingsCard({ plugin: p, owner, refresh }: {
  plugin: PluginRegistration; owner: string; refresh: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [review, setReview] = useState(false);
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
      setReview(false); setConfigure(false); setRemove(false); setLogs(null);
    } catch {
      // Never echo an error body that could contain submitted configuration.
      setError(action === 'configuration'
        ? 'Configuration was not activated. The server attempts to restore the last working configuration; check the current values and health below.'
        : `Could not ${action === 'grants' ? 'revoke grants' : action} plugin. Check its health and retry.`);
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

  return <section aria-label={`${d.name} plugin`}>
    <h3>{d.name}</h3>
    <PluginMetadata plugin={p} />
    {error && <p role="alert">{error}</p>}
    <fieldset disabled={busy} style={{ border: 0, padding: 0 }}>
      <legend>Manage {d.name}</legend>
      <SettingRow label="Lifecycle" block>
        <div className="remote-row-actions">
          <button type="button" className="vscode-btn" disabled={p.enabled || blocked} onClick={() => setReview(true)}>Enable</button>
          <button type="button" className="vscode-btn" disabled={!p.enabled} onClick={() => { void mutate('disable'); }}>Disable</button>
          <button type="button" className="vscode-btn" disabled={!p.enabled || blocked} onClick={() => { void mutate('retry'); }}>Retry</button>
          <button type="button" className="vscode-btn" disabled={!p.enabled || blocked} onClick={() => { void mutate('restart'); }}>Restart</button>
          <button type="button" className="vscode-btn" onClick={() => setConfigure(!configure)}>Configure</button>
          <button type="button" className="vscode-btn" disabled={!p.grants?.length} onClick={() => { void mutate('grants', { grants: [] }); }}>Revoke grants</button>
          <button type="button" className="vscode-btn" disabled={p.enabled} onClick={() => setRemove(true)}>Remove data</button>
        </div>
      </SettingRow>
      {review && <SettingRow label="Review grants before enabling" block>
        <p>This native executable runs on {owner} with {d.scope} scope. Approve checksum <code>{p.checksum}</code> and these requested grants:</p>
        <ul>{(d.requestedGrants ?? []).map((grant) => <li key={grant}>{grant}</li>)}</ul>
        {!d.requestedGrants?.length && <p>No grants requested.</p>}
        <button type="button" className="vscode-btn" onClick={() => { void mutate('enable', { approval: p.approval, grants: d.requestedGrants ?? [] }); }}>Approve grants and enable</button>
        <button type="button" className="vscode-btn" onClick={() => setReview(false)}>Cancel approval</button>
      </SettingRow>}
      {configure && <PluginConfiguration key={JSON.stringify(p.configuration)} plugin={p} save={(input) => mutate('configuration', input)} />}
      {remove && <SettingRow label="Permanently remove plugin data" block>
        <p>Delete configuration, secrets, grants, and private data for {d.id} on {owner}. This cannot be undone. The executable is not deleted.</p>
        <button type="button" className="vscode-btn" onClick={() => { void mutate('remove-data'); }}>Confirm permanent removal</button>
        <button type="button" className="vscode-btn" onClick={() => setRemove(false)}>Cancel removal</button>
      </SettingRow>}
      <SettingRow label="Recent stderr" desc="Bounded diagnostic output from the selected owner." block>
        <button type="button" className="vscode-btn" onClick={() => { void loadLogs(); }}>Load recent stderr</button>
        {logs !== null && <pre aria-label="Recent stderr" style={{ maxHeight: 240, overflow: 'auto', whiteSpace: 'pre-wrap' }}>{logs || 'No recent stderr.'}</pre>}
      </SettingRow>
    </fieldset>
  </section>;
}
