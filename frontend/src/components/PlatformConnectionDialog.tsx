import { useRef, useState } from 'react';
import type { ConnectionCheck, ConnectionInput, ConnectionPlatform, PlatformConnection } from '../lib/platformConnections';
import { connectionStatusLabel } from '../lib/platformConnections';
import { useUnsavedChangesGuard } from '../lib/useUnsavedChangesGuard';
import { Button, CheckboxField, SelectField, TextField } from './Control';
import { CopyButton } from './CopyButton';
import { InlineAlert } from './InlineAlert';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { ModalFooter } from './ModalFooter';
import styles from './PlatformConnections.module.css';

type Props = {

  recoveredInput?: ConnectionInput;
  onAbandon?: () => Promise<void>;
  platforms: ConnectionPlatform[];
  connections: PlatformConnection[];
  connection?: PlatformConnection;
  onCheck: (input: ConnectionInput) => Promise<ConnectionCheck>;
  onSave: (input: ConnectionInput, id?: string) => Promise<void>;
  onClose: () => void;
};

function initialInput(connection: PlatformConnection | undefined, platforms: ConnectionPlatform[]): ConnectionInput {
  return {
    kind: connection?.kind ?? platforms[0]?.id ?? '',
    name: connection?.name ?? '',
    configuration: connection?.configuration ?? 'existing',
    directory: connection?.directory ?? '',
    enabled: connection?.enabled ?? true,
  };
}

function saveLabel(editing: boolean, check: ConnectionCheck | null) {
  if (editing) return 'Save changes';
  if (!check) return 'Check setup';
  return check.status === 'ready' ? 'Add platform' : 'Save for later';
}

function setupKey(input: ConnectionInput) {
  return JSON.stringify([input.kind, input.configuration, input.directory.trim()]);
}

export function PlatformConnectionDialog({ platforms, connections, connection, recoveredInput, onAbandon, onCheck, onSave, onClose }: Props) {
  const [initial] = useState(() => !connection && recoveredInput ? recoveredInput : initialInput(connection, platforms));
  const [input, setInput] = useState<ConnectionInput>(initial);
  const [check, setCheck] = useState<ConnectionCheck | null>(null);
  const [operation, setOperation] = useState<'checking' | 'saving' | null>(null);
  const busy = operation !== null;
  const lock = useRef(false);
  const reservation = useRef<string | undefined>(initial.reservationId);
  const [error, setError] = useState('');
  const platform = platforms.find(value => value.id === input.kind);
  const dirty = JSON.stringify(input) !== JSON.stringify(initial);
  const { confirmDiscard } = useUnsavedChangesGuard(dirty);
  const duplicate = connections.some(value => value.id !== connection?.id && value.name.toLocaleLowerCase() === input.name.trim().toLocaleLowerCase());
  const valid = !!platform && !!input.name.trim() && !duplicate;
  async function close() {
    if (lock.current || !await confirmDiscard()) return;
    try { await onAbandon?.(); onClose(); }
    catch { setError('Could not cancel setup. Retry when the owner is connected.'); }
  }

  function update(patch: Partial<ConnectionInput>) {
    if (setupKey({ ...input, ...patch }) !== setupKey(input)) {
      reservation.current = undefined;
      patch = { ...patch, reservationId: undefined };
    }
    setInput(value => ({ ...value, ...patch }));
    setCheck(null);
    setError('');
  }

  function requestInput(): ConnectionInput {
    const value = { ...input, name: input.name.trim(), directory: input.directory.trim() };
    delete value.reservationId;
    return reservation.current ? { ...value, reservationId: reservation.current } : value;
  }

  async function checkSetup(value: ConnectionInput) {
    const result = await onCheck(connection ? { ...value, connectionId: connection.id } : value);
    if (value.reservationId && result.reservationId !== value.reservationId) {
      setCheck(null);
      throw new Error('The setup reservation changed. Retry the check or close this dialog and start again.');
    }
    reservation.current = result.reservationId;
    setCheck(result);
  }

  async function submit() {
    if (lock.current || !valid) return;
    lock.current = true;
    setOperation(connection || check ? 'saving' : 'checking');
    setError('');
    try {
      const value = requestInput();
      if (connection || check) {
        // Account/status observations are not client-authoritative save fields.
        await onSave(value, connection?.id);
        onClose();
      } else await checkSetup(value);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save platform connection. Try again.');
    } finally { lock.current = false; setOperation(null); }
  }

  async function inspect() {
    if (lock.current || !valid) return;
    lock.current = true;
    setOperation('checking');
    setError('');
    try { await checkSetup(requestInput()); }
    catch (err) { setError(err instanceof Error ? err.message : 'Could not check platform setup. Try again.'); }
    finally { lock.current = false; setOperation(null); }
  }

  return <Modal label={connection ? 'Edit platform' : 'Add platform'} onClose={() => { void close(); }} canClose={!busy}>
    <ModalHeader title={connection ? 'Edit platform' : 'Add platform'}
      description={connection ? 'Renaming a connection does not move its sessions or change its account.' : 'Connect a coding tool. Add it again to keep work and personal accounts separate.'}
      onClose={() => { void close(); }} closeLabel="Close platform setup" canClose={!busy} />
    <form onSubmit={event => { event.preventDefault(); void submit(); }}>
      <div className={styles.form}>
        {!connection && <ol className={styles.steps} aria-label="Setup progress">
          <li aria-current={!check ? 'step' : undefined}>1. Configure</li>
          <li aria-current={check ? 'step' : undefined}>2. Check setup</li>
        </ol>}
        {!check ? <ConnectionFields input={input} platform={platform} platforms={platforms} editing={!!connection} duplicate={duplicate} busy={busy} update={update} />
          : <ConnectionCheckResult input={input} platform={platform} check={check} />}
        {error && <InlineAlert>{error}</InlineAlert>}
      </div>
      <ModalFooter label="Platform setup actions">
        <Button type="button" onClick={() => { void close(); }} disabled={busy}>Cancel</Button>
        {check && <Button type="button" onClick={() => setCheck(null)} disabled={busy}>Back</Button>}
        {(connection || check) && <Button type="button" disabled={!valid || busy} onClick={() => { void inspect(); }}>{check ? 'Check again' : 'Check setup'}</Button>}
        <Button type="submit" variant="accent" disabled={!valid || busy} aria-busy={busy}>
          {operation === 'checking' ? 'Checking…' : operation === 'saving' ? 'Saving…' : saveLabel(!!connection, check)}
        </Button>
      </ModalFooter>
    </form>
  </Modal>;
}

function ConnectionFields({ input, platform, platforms, editing, duplicate, busy, update }: {
  input: ConnectionInput;
  platform: ConnectionPlatform | undefined;
  platforms: ConnectionPlatform[];
  editing: boolean;
  duplicate: boolean;
  busy: boolean;
  update: (patch: Partial<ConnectionInput>) => void;
}) {
  return <>
    <label className={styles.field}>Platform
      <SelectField value={input.kind} disabled={editing || busy} onChange={event => update({ kind: event.target.value, configuration: 'existing', directory: '' })}>
        {platforms.map(value => <option key={value.id} value={value.id}>{value.label}</option>)}
      </SelectField>
    </label>
    <label className={styles.field}>Name
      <TextField data-autofocus value={input.name} placeholder={platform ? `${platform.label} Work` : 'Work account'} maxLength={80} required disabled={busy}
        onChange={event => update({ name: event.target.value })} aria-invalid={duplicate || undefined} aria-describedby="connection-name-help" />
    </label>
    <span id="connection-name-help" className={styles.hint}>{duplicate ? 'A connection with this name already exists.' : 'Shown in the session picker. For example, Claude Personal or Codex Work.'}</span>
    <label className={styles.field}>Configuration
      <SelectField value={input.configuration} disabled={editing || busy} onChange={event => update({ configuration: event.target.value as ConnectionInput['configuration'], directory: '' })}>
        <option value="existing">Use an existing configuration</option>
        {(platform?.supportsIsolation || input.configuration === 'isolated') && <option value="isolated">Create a separate configuration</option>}
      </SelectField>
    </label>
    {input.configuration === 'existing' ? <>
      <label className={styles.field}>Configuration directory <span className={styles.hint}>(optional)</span>
        <TextField value={input.directory} disabled={editing || busy} placeholder={platform?.existingDirectory} onChange={event => update({ directory: event.target.value })} />
      </label>
      <p className={styles.hint}>Leave empty to use this machine’s default configuration. Credentials stay with the native tool; Ocman does not copy them.</p>
    </> : <p className={styles.note}>{platform?.isolationNote} You will sign in separately. Existing credentials will not be copied.</p>}
    {editing && <>
      <p className={styles.hint}>To use a different configuration or account, add a new connection. Existing sessions stay on this one.</p>
      <label className={styles.checkbox}><CheckboxField checked={input.enabled} disabled={busy} onChange={event => update({ enabled: event.target.checked })} /> Available for new sessions</label>
    </>}
  </>;
}

function ConnectionCheckResult({ input, platform, check }: { input: ConnectionInput; platform: ConnectionPlatform | undefined; check: ConnectionCheck }) {
  return <>
    <div className={styles.summary}><strong>{input.name.trim()}</strong><span>{platform?.label} · {input.configuration === 'isolated' ? 'Separate configuration' : 'Existing configuration'}</span></div>
    <strong role="status">{connectionStatusLabel[check.status]}</strong>
    {check.account && <p className={styles.note}>Account: {check.account}</p>}
    {check.directory && <p className={styles.path}>{check.directory}</p>}
    <ul className={styles.checks}>{check.checks.map((value, index) => <li key={index}><i className={`bi ${value.ok ? 'bi-check-circle' : 'bi-exclamation-circle'}`} aria-hidden="true" /><div><strong>{value.label}</strong><span>{value.detail}</span></div></li>)}</ul>
    {check.instructions && <p className={styles.note}>{check.instructions}</p>}
    {check.command && <div className={styles.command}><code>{check.command}</code><CopyButton text={check.command} label="Copy sign-in command" /></div>}
    {check.status !== 'ready' && <p className={styles.hint}>You can save this connection now and finish setup later. It cannot start sessions until setup succeeds.</p>}
    <p className={styles.hint}>A session stays on the account you select. Ocman will not switch it to another connection automatically.</p>
  </>;
}
