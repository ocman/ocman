import type { PluginSettingsView } from './PluginSettingsDrawer';
import { hasConversationCapability, type PluginRegistration } from '../lib/plugins';
import { usePluginManagement, type PluginManagement } from '../lib/usePluginManagement';
import { SettingRow } from './SettingRow';
import { PluginConfiguration } from './PluginConfiguration';
import { PluginDeliveryBacklog } from './PluginDeliveryBacklog';
import { PluginAgentTools, PluginAgentToolsDisclosure } from './PluginAgentTools';
import { PluginLogs } from './PluginLogs';
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

type Props = {
  plugin: PluginRegistration; owner: string; refresh: () => Promise<void>;
  view?: PluginSettingsView; onDirtyChange?: (dirty: boolean) => void;
  onCancel?: () => void;
  onOpenSettings?: () => void;
};

export function PluginSettingsCard(props: Props) {
  const management = usePluginManagement(props.plugin, props.owner, props.refresh, props.onOpenSettings);
  return <PluginSettingsContent {...props} management={management} />;
}

export function PluginSettingsContent({ plugin: p, owner, view = 'permissions', onDirtyChange, onCancel, onOpenSettings, management }: Omit<Props, 'refresh'> & { management: PluginManagement }) {
  const { busy, error, review, setReview, reviewEnable, configurationAttempt, configurationSaved, configurationFocus, configurationRequested, enableNeedsConfiguration, pendingAction, remove, setRemove, mutate } = management;
  const d = p.description;
  const blocked = p.removed || p.health.status === 'conflict';
  const permissions = view === 'permissions';
  const diagnostics = view === 'diagnostics';
  const configuration = view === 'settings' || (!onOpenSettings && configurationRequested);

  return <section aria-label={`${d.name} plugin`}>
    {diagnostics && p.health.lastError && <div className={styles.healthError}><i className="bi bi-exclamation-triangle" aria-hidden="true" /><div>
      <strong>Plugin reported an error</strong><p>{p.health.lastError}</p>
      {onOpenSettings && <Button type="button" size="small" disabled={busy} onClick={onOpenSettings}>Open settings</Button>}
    </div></div>}
    {permissions && <>
      <SettingRow label="Execution scope">{d.scope}</SettingRow>
      <SettingRow label="Requested grants">{d.requestedGrants?.join(', ') || 'None'}</SettingRow>
      <SettingRow label="Current grants">{p.grants?.join(', ') || 'None'}</SettingRow>
      {d.ui && <SettingRow label="Interface">In-app UI · @ocman/plugin-ui {d.ui.sdk}</SettingRow>}
    </>}
    {configurationSaved && <p role="status">Configuration saved.</p>}
    {error && !configuration && <p role="alert">{error}</p>}
    <fieldset disabled={busy} className={styles.management} aria-label={`${view} for ${d.name}`}>
      {(permissions || diagnostics) && <SettingRow label={diagnostics ? 'Recovery' : 'Lifecycle'} block>
        <ButtonGroup label="Plugin lifecycle actions">
          {permissions && <>
            <Button type="button" size="small" variant="accent" disabled={p.enabled || blocked} onClick={reviewEnable}>Enable</Button>
            <Button type="button" size="small" disabled={blocked || !d.requestedGrants?.some((grant) => !p.grants?.includes(grant))} onClick={() => setReview('grants')}>Grant permissions</Button>
            <Button type="button" size="small" disabled={!p.enabled} onClick={() => { void mutate('disable'); }}>Disable</Button>
          </>}
          {diagnostics && <>
            <Button type="button" size="small" disabled={!p.enabled || blocked} onClick={() => { void mutate('retry'); }}>Retry</Button>
            <Button type="button" size="small" disabled={!p.enabled || blocked} onClick={() => { void mutate('restart'); }}>Restart</Button>
          </>}
          {permissions && <>
            <Button type="button" size="small" variant="danger" disabled={!p.grants?.length} onClick={() => { void mutate('grants', { grants: [] }); }}>Revoke grants</Button>
            <Button type="button" size="small" variant="danger" disabled={p.enabled} onClick={() => setRemove(true)}>Remove data</Button>
          </>}
        </ButtonGroup>
      </SettingRow>}
      {permissions && review && <SettingRow label={review === 'enable' ? 'Review grants before enabling' : 'Review grants'} block>
        <p>This native executable runs on {owner} with {d.scope} scope. Approve checksum <code>{p.checksum}</code> and these requested grants:</p>
        <ul>{(d.requestedGrants ?? []).map((grant) => <li key={grant}>{grant}</li>)}</ul>
        {!d.requestedGrants?.length && <p>No grants requested.</p>}
        <PluginAgentToolsDisclosure plugin={p} owner={owner} />
        <PluginUIDisclosure plugin={p} owner={owner} />
        <ButtonGroup label="Plugin approval actions">
          <Button type="button" variant="accent" onClick={() => { void mutate(review, { ...(review === 'enable' ? { approval: p.approval } : {}), grants: d.requestedGrants ?? [] }); }}>{review === 'enable' ? 'Approve grants and enable' : 'Approve grants'}</Button>
          <Button type="button" onClick={() => setReview(null)}>Cancel approval</Button>
        </ButtonGroup>
      </SettingRow>}
      {configuration && <PluginConfiguration key={`${configurationAttempt}:${JSON.stringify(p.configuration)}`} plugin={p}
        onDirtyChange={onDirtyChange} onCancel={onCancel} error={error} saving={pendingAction === 'configuration'} busy={busy}
        focusRequest={configurationFocus} enableNeedsConfiguration={enableNeedsConfiguration} save={(input) => mutate('configuration', input)} />}
      {permissions && remove && <SettingRow label="Permanently remove plugin data" block>
        <p>Delete configuration, secrets, grants, and private data for {d.id} on {owner}. This cannot be undone. The executable is not deleted.</p>
        <ButtonGroup label="Plugin removal actions">
          <Button type="button" variant="danger" onClick={() => { void mutate('remove-data'); }}>Confirm permanent removal</Button>
          <Button type="button" onClick={() => setRemove(false)}>Cancel removal</Button>
        </ButtonGroup>
      </SettingRow>}
      {diagnostics && hasConversationCapability(p) && <PluginDeliveryBacklog plugin={p} owner={owner} />}
      {permissions && <PluginAgentTools plugin={p} owner={owner} />}
    </fieldset>
    {diagnostics && <>
      <dl className={styles.healthSummary}><div><dt>Status</dt><dd>{p.health.status}</dd></div><div><dt>Restart count</dt><dd>{p.health.restartCount}</dd></div></dl>
      <PluginLogs owner={owner} pluginId={d.id} />
      <details className={styles.technical}><summary>Technical details</summary><PluginMetadata plugin={p} /></details>
    </>}
  </section>;
}
