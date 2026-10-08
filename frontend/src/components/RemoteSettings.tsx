import { useCallback, useEffect, useState } from 'react';
import styles from './RemoteSettings.module.css';
import { api } from '../lib/api';
import type { RemoteStatus, RemoteAccessStatus } from '../lib/api.types';
import { Button, ButtonGroup, TextField } from './Control';
import { CheckboxField } from './CheckboxField';
import { DataTable } from './DataTable';
import { Drawer } from './Drawer';
import { ModalFooter } from './ModalFooter';

/**
 * RemoteSettings is the hub-side remote-management UI (multi-remote
 * support, FR-14). It shows this instance's own remote-access details
 * ("This machine", non-removable) plus a list of attached remotes with
 * add / edit / reconnect / remove actions. Tokens are never displayed
 * except via the explicit reveal action on this machine's own token.
 */
export function RemoteSettings() {
  const [access, setAccess] = useState<RemoteAccessStatus | null>(null);
  const [remotes, setRemotes] = useState<RemoteStatus[]>([]);
  const [revealed, setRevealed] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<RemoteStatus | null>(null);

  const refresh = useCallback(() => {
    api.listRemotes().then(setRemotes).catch(() => { /* empty on single-host */ });
  }, []);

  useEffect(() => {
    api.remoteAccess().then(setAccess).catch(() => setAccess(null));
    refresh();
  }, [refresh]);

  async function revealToken() {
    try {
      const { token } = await api.revealRemoteToken();
      setRevealed(token);
    } catch {
      setError('Failed to reveal token.');
    }
  }

  return (
    <div className={styles.settings} data-testid="remote-settings">
      {error && <div className={styles.error} role="alert">{error}</div>}

      <DataTable framed aria-label="Machines" className={styles.table}>
        <thead>
          <tr><th scope="col">Machine</th><th scope="col">Connection</th><th scope="col">Status</th><th scope="col">Actions</th></tr>
        </thead>
        <tbody>
          {access && (
            <tr>
              <td>
                <div className={styles.name}>This machine</div>
                <div className={styles.meta}>ID {access.instanceId || '—'}</div>
              </td>
              <td>
                <div className={styles.meta}>
                  {access.listening
                    ? `listening on ${access.listenAddr}${access.tls ? ' (TLS)' : ' (no TLS)'}`
                    : 'remote access off (start with -remote-listen)'}
                </div>
              </td>
              <td><span className={styles.health} data-health={access.listening ? 'connected' : 'disabled'}>{access.listening ? 'listening' : 'off'}</span></td>
              <td>
                <ButtonGroup label="This machine actions" className={styles.actions}>
                  {revealed ? (
                    <TextField
                      className={styles.token}
                      readOnly
                      value={revealed}
                      onFocus={(e) => e.currentTarget.select()}
                      aria-label="Remote-access token"
                    />
                  ) : (
                    <Button
                      type="button"
                      size="small"
                      onClick={() => { void revealToken(); }}
                      disabled={!access.tokenSet}
                    >
                      Reveal token
                    </Button>
                  )}
                </ButtonGroup>
              </td>
            </tr>
          )}
          {remotes.map((r) => (
            <RemoteRow key={r.localId} remote={r} onChanged={refresh} onEdit={() => setEditing(r)} />
          ))}
        </tbody>
      </DataTable>

      <AddRemoteForm onAdded={refresh} />
      {editing && <EditRemoteForm remote={editing} onDone={() => { setEditing(null); refresh(); }} onCancel={() => setEditing(null)} />}
    </div>
  );
}

function RemoteRow({ remote, onChanged, onEdit }: { remote: RemoteStatus; onChanged: () => void; onEdit: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function act(fn: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
      onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to update remote.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <tr data-testid="remote-row">
      <td><div className={styles.name}>{remote.displayName || remote.hostname || remote.address}</div></td>
      <td>
        <div className={styles.meta}>
          {remote.address}
        </div>
        <div className={styles.meta}>
          {remote.hostname || ''}
          {remote.sessionCount ? ` · ${remote.sessionCount} sessions` : ''}
          {remote.lastSeen ? ` · seen ${new Date(remote.lastSeen).toLocaleString()}` : ''}
        </div>
        {error && <div className={styles.error} role="alert">{error}</div>}
      </td>
      <td><span className={styles.health} data-health={remote.enabled ? remote.health || 'unknown' : 'disabled'}>
        {remote.enabled ? remote.health || 'unknown' : 'disabled'}
      </span></td>
      <td>
        <ButtonGroup label={`${remote.displayName || remote.hostname || remote.address} actions`} className={styles.actions}>
          <Button type="button" size="small" disabled={busy}
            onClick={() => { void act(() => api.updateRemote(remote.localId, {
              address: remote.address,
              displayName: remote.displayName,
              enabled: !remote.enabled,
            })); }}>
            {remote.enabled ? 'Disable' : 'Enable'}
          </Button>
          <Button type="button" size="small" disabled={busy || !remote.enabled}
            onClick={() => { void act(() => api.reconnectRemote(remote.localId)); }}>
            Reconnect
          </Button>
          <Button type="button" size="small" disabled={busy}
            onClick={onEdit}>
            Edit
          </Button>
          <Button type="button" size="small" variant="danger" disabled={busy}
            onClick={() => { void act(() => api.removeRemote(remote.localId)); }}>
            Remove
          </Button>
        </ButtonGroup>
      </td>
    </tr>
  );
}

function AddRemoteForm({ onAdded }: { onAdded: () => void }) {
  const [address, setAddress] = useState('');
  const [token, setToken] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      await api.addRemote({ address: address.trim(), token: token.trim(), displayName: displayName.trim() });
      setAddress('');
      setToken('');
      setDisplayName('');
      onAdded();
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Failed to add remote.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className={styles.form} onSubmit={(e) => { void submit(e); }}>
      <div className={styles.title}>Attach a remote</div>
      {err && <div className={styles.error} role="alert">{err}</div>}
      <TextField className={styles.input} placeholder="host:port (e.g. ws.local:8230)"
        value={address} onChange={(e) => setAddress(e.target.value)} aria-label="Remote address" />
      <TextField className={styles.input} placeholder="remote-access token" type="password"
        value={token} onChange={(e) => setToken(e.target.value)} aria-label="Remote-access token" />
      <TextField className={styles.input} placeholder="display name (optional)"
        value={displayName} onChange={(e) => setDisplayName(e.target.value)} aria-label="Display name" />
      <Button type="submit" variant="accent" aria-busy={busy}
        disabled={busy || !address.trim() || !token.trim()}>
        {busy ? 'Connecting…' : 'Add remote'}
      </Button>
    </form>
  );
}

function EditRemoteForm({ remote, onDone, onCancel }: {
  remote: RemoteStatus;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [displayName, setDisplayName] = useState(remote.displayName);
  const [address, setAddress] = useState(remote.address);
  const [enabled, setEnabled] = useState(remote.enabled);
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.updateRemote(remote.localId, {
        address: address.trim(),
        displayName: displayName.trim(),
        enabled,
        token: token.trim() ? token.trim() : null,
      });
      onDone();
    } catch {
      setError('Failed to save remote.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Drawer title="Edit remote" onClose={onCancel} canClose={!busy}>
    <form className={styles.editForm} onSubmit={(e) => { void submit(e); }}>
      <label className={styles.field}>Display name
        <TextField disabled={busy} value={displayName} data-autofocus
          onChange={(e) => setDisplayName(e.target.value)} placeholder="display name" />
      </label>
      <label className={styles.field}>Remote address
        <TextField disabled={busy} value={address}
          onChange={(e) => setAddress(e.target.value)} placeholder="host:port" />
      </label>
      <label className={styles.field}>Replace token
        <TextField disabled={busy} type="password" value={token} aria-describedby="remote-token-help"
          onChange={(e) => setToken(e.target.value)} placeholder="remote-access token" />
        <span id="remote-token-help" className={styles.help}>Leave blank to keep the current token.</span>
      </label>
      <CheckboxField label="Enabled" disabled={busy} checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
      {error && <p className={styles.error} role="alert">{error}</p>}
      <ModalFooter label="Edit remote actions">
        <Button type="submit" variant="accent" aria-busy={busy} disabled={busy}>Save</Button>
        <Button type="button" disabled={busy} onClick={onCancel}>Cancel</Button>
      </ModalFooter>
    </form>
    </Drawer>
  );
}
