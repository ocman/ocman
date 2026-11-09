import { useRef, useState } from 'react';
import type { ConnectionCheck, ConnectionInput, ConnectionPlatform, PlatformConnection } from '../lib/platformConnections';
import { connectionStatusLabel } from '../lib/platformConnections';
import { useConfirm } from '../lib/useConfirm';
import { Button, ButtonGroup } from './Control';
import { DataTableGroup, DataTableRow, DataTableRowButton } from './DataTable';
import { InlineAlert } from './InlineAlert';
import { LoadingState } from './LoadingState';
import { PageState } from './PageState';
import { PlatformConnectionDialog } from './PlatformConnectionDialog';
import { PlatformLogo } from './PlatformBadge';
import styles from './PlatformConnections.module.css';

export type PlatformConnectionsProps = {
  /** Null means initial loading, not an empty installation. */
  connections: PlatformConnection[] | null;
  platforms: ConnectionPlatform[];
  machineName: string;
  error?: string;
  readOnlyReason?: string;
  onboarding?: boolean;
  onRetry: () => void;
  onSave: (input: ConnectionInput, id?: string) => Promise<void>;
  onCheck: (input: ConnectionInput) => Promise<ConnectionCheck>;
  onRemove: (id: string) => Promise<void>;
  onDefault: (id: string) => Promise<void>;
  onEnabledChange: (id: string, enabled: boolean) => Promise<void>;
  onSkip?: () => void;
  onContinue?: () => void;
};

/** Shared by Settings and first-run onboarding; runtime and owner calls stay outside the view. */
export function PlatformConnections({ connections, platforms, machineName, error, readOnlyReason, onboarding = false, onRetry, onSave, onCheck, onRemove, onDefault, onEnabledChange, onSkip, onContinue }: PlatformConnectionsProps) {
  const [editor, setEditor] = useState<PlatformConnection | 'new' | null>(null);
  const [pending, setPending] = useState<string | null>(null);
  const lock = useRef(false);
  const [actionError, setActionError] = useState('');
  const [notice, setNotice] = useState('');
  const confirm = useConfirm();
  const writable = !readOnlyReason && pending === null;

  async function mutate(id: string, action: () => Promise<void>, message: string) {
    if (lock.current) return;
    lock.current = true;
    setPending(id);
    setActionError('');
    setNotice('');
    try { await action(); setNotice(message); }
    catch (err) { setActionError(err instanceof Error ? err.message : 'Could not update platform connection. Try again.'); }
    finally { lock.current = false; setPending(null); }
  }

  async function remove(connection: PlatformConnection) {
    const accepted = await confirm({ title: `Remove ${connection.name}?`, description: 'This removes the connection from new-session choices. Session history and native credentials are kept. Active work must finish before the connection is detached.', confirmLabel: 'Remove connection', destructive: true });
    if (accepted) await mutate(connection.id, () => onRemove(connection.id), `${connection.name} removed.`);
  }

  if (connections === null) return error
    ? <PageState compact error title="Platforms unavailable" description={error} onRetry={onRetry} />
    : <LoadingState>Loading platform connections…</LoadingState>;

  return <section className={styles.root} aria-label="Platform connections">
    <div className={styles.toolbar}>
      <div><strong>{onboarding ? 'Connect your first platform' : 'Platform connections'}</strong><p className={styles.hint}>{machineName} · Keep work and personal accounts separate.</p></div>
      {connections.length > 0 && <Button variant="accent" disabled={!writable || platforms.length === 0} onClick={() => setEditor('new')}>Add platform</Button>}
    </div>
    {readOnlyReason && <InlineAlert>{readOnlyReason}</InlineAlert>}
    {error && <InlineAlert>{error} <Button onClick={onRetry} size="small">Retry</Button></InlineAlert>}
    {connections.length === 0 ? <PageState compact icon="bi-plug" title="No platforms connected"
      description="Connect OpenCode, Claude Code or Codex. Already signed in on this machine? Reuse that configuration, or create a separate one for another account.">
      <Button variant="accent" disabled={!writable || platforms.length === 0} onClick={() => setEditor('new')}>Add platform</Button>
    </PageState> : <DataTableGroup label={machineName} noun="connections" count={connections.length}>
      {connections.map(connection => <DataTableRow key={connection.id}
        primary={<div className={styles.name}><PlatformLogo platform={connection.kind} /><DataTableRowButton disabled={!writable} onClick={() => setEditor(connection)}>{connection.name}</DataTableRowButton>{connection.isDefault && <span className={styles.badge}>{connection.enabled ? 'Default' : 'Default (disabled)'}</span>}</div>}
        secondary={<div className={styles.details}><span>{platforms.find(value => value.id === connection.kind)?.label ?? connection.kind}</span><span>{connection.account ?? 'Account not verified'}</span><span className={styles.path} title={connection.directory}>{connection.configuration === 'isolated' ? 'Separate configuration' : 'Existing configuration'}{connection.directory ? ` · ${connection.directory}` : ''}</span></div>}
        meta={<div className={styles.rowMeta}>
          <span className={styles.status} data-ready={connection.enabled && connection.status === 'ready' ? '' : undefined}>{!connection.enabled ? 'Disabled' : connectionStatusLabel[connection.status]}</span>
          <ButtonGroup label={`${connection.name} actions`}>
            {!connection.isDefault && <Button size="small" disabled={!writable || !connection.enabled || connection.status !== 'ready'} onClick={() => { void mutate(connection.id, () => onDefault(connection.id), `${connection.name} is now the default.`); }}>Set default</Button>}
            <Button size="small" disabled={!writable} onClick={() => setEditor(connection)} aria-label={`Edit ${connection.name}`}>Edit</Button>
            <Button size="small" disabled={!writable} aria-label={`${connection.enabled ? 'Disable' : 'Enable'} ${connection.name}`}
              onClick={() => { void mutate(connection.id, () => onEnabledChange(connection.id, !connection.enabled), `${connection.name} ${connection.enabled ? 'disabled' : 'enabled'}.`); }}>{connection.enabled ? 'Disable' : 'Enable'}</Button>
            <Button size="small" variant="danger" disabled={!writable} onClick={() => { void remove(connection); }} aria-label={`Remove ${connection.name}`}>Remove</Button>
          </ButtonGroup>
        </div>} />)}
    </DataTableGroup>}
    {connections.length > 0 && <p className={styles.hint}>Choose a named connection when starting a session. Disabling a connection prevents new sessions without removing its history or credentials. Existing sessions keep their original connection; Ocman never switches accounts automatically.</p>}
    {actionError && <InlineAlert>{actionError}</InlineAlert>}
    {notice && <p role="status">{notice}</p>}
    {onboarding && <div className={styles.onboardingActions}>
      <span className={styles.hint}>You can add more platforms in Settings at any time.</span>
      <ButtonGroup label="Onboarding actions">
        {onSkip && <Button onClick={onSkip}>Set up later</Button>}
        {onContinue && <Button variant="accent" disabled={!connections.some(value => value.enabled && value.status === 'ready')} onClick={onContinue}>Choose a project</Button>}
      </ButtonGroup>
    </div>}
    {editor && <PlatformConnectionDialog platforms={platforms} connections={connections} connection={editor === 'new' ? undefined : editor} onCheck={onCheck} onSave={onSave} onClose={() => setEditor(null)} />}
  </section>;
}
