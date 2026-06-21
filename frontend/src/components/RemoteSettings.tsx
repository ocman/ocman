import { useCallback, useEffect, useState } from 'react';
import './RemoteSettings.css';
import { api } from '../lib/api';
import type { RemoteStatus, RemoteAccessStatus } from '../lib/api.types';

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
    <div className="remote-settings" data-testid="remote-settings">
      {error && <div className="remote-settings-error" role="alert">{error}</div>}

      {access && (
        <div className="remote-row remote-row-self">
          <div className="remote-row-main">
            <div className="remote-row-name">This machine</div>
            <div className="remote-row-meta mono">
              ID {access.instanceId || '—'}
              {access.listening
                ? ` · listening on ${access.listenAddr}${access.tls ? ' (TLS)' : ' (no TLS)'}`
                : ' · remote access off (start with -remote-listen)'}
            </div>
          </div>
          <div className="remote-row-actions">
            {revealed ? (
              <input
                className="remote-token-reveal mono"
                readOnly
                value={revealed}
                onFocus={(e) => e.currentTarget.select()}
                aria-label="Remote-access token"
              />
            ) : (
              <button
                type="button"
                className="remote-btn"
                onClick={() => { void revealToken(); }}
                disabled={!access.tokenSet}
              >
                Reveal token
              </button>
            )}
          </div>
        </div>
      )}

      {remotes.map((r) => (
        <RemoteRow key={r.localId} remote={r} onChanged={refresh} />
      ))}

      <AddRemoteForm onAdded={refresh} />
    </div>
  );
}

function RemoteRow({ remote, onChanged }: { remote: RemoteStatus; onChanged: () => void }) {
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);

  async function act(fn: () => Promise<unknown>) {
    setBusy(true);
    try {
      await fn();
      onChanged();
    } finally {
      setBusy(false);
    }
  }

  if (editing) {
    return (
      <EditRemoteForm
        remote={remote}
        onDone={() => { setEditing(false); onChanged(); }}
        onCancel={() => setEditing(false)}
      />
    );
  }

  return (
    <div className="remote-row" data-testid="remote-row">
      <div className="remote-row-main">
        <div className="remote-row-name">
          {remote.displayName || remote.hostname || remote.address}
          <span className={`remote-health remote-health-${remote.health || 'unknown'}`}>
            {remote.health || 'unknown'}
          </span>
          {!remote.enabled && <span className="remote-health remote-health-disabled">disabled</span>}
        </div>
        <div className="remote-row-meta mono">
          {remote.address}
          {remote.hostname ? ` · ${remote.hostname}` : ''}
          {remote.sessionCount ? ` · ${remote.sessionCount} sessions` : ''}
          {remote.lastSeen ? ` · seen ${new Date(remote.lastSeen).toLocaleString()}` : ''}
        </div>
      </div>
      <div className="remote-row-actions">
        <button type="button" className="remote-btn" disabled={busy}
          onClick={() => { void act(() => api.reconnectRemote(remote.localId)); }}>
          Reconnect
        </button>
        <button type="button" className="remote-btn" disabled={busy}
          onClick={() => setEditing(true)}>
          Edit
        </button>
        <button type="button" className="remote-btn remote-btn-danger" disabled={busy}
          onClick={() => { void act(() => api.removeRemote(remote.localId)); }}>
          Remove
        </button>
      </div>
    </div>
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
    <form className="remote-add-form" onSubmit={(e) => { void submit(e); }}>
      <div className="remote-add-title">Attach a remote</div>
      {err && <div className="remote-settings-error" role="alert">{err}</div>}
      <input className="remote-input" placeholder="host:port (e.g. ws.local:8230)"
        value={address} onChange={(e) => setAddress(e.target.value)} aria-label="Remote address" />
      <input className="remote-input" placeholder="remote-access token" type="password"
        value={token} onChange={(e) => setToken(e.target.value)} aria-label="Remote-access token" />
      <input className="remote-input" placeholder="display name (optional)"
        value={displayName} onChange={(e) => setDisplayName(e.target.value)} aria-label="Display name" />
      <button type="submit" className="remote-btn remote-btn-primary"
        disabled={busy || !address.trim() || !token.trim()}>
        {busy ? 'Connecting…' : 'Add remote'}
      </button>
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

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.updateRemote(remote.localId, {
        address: address.trim(),
        displayName: displayName.trim(),
        enabled,
        token: token.trim() ? token.trim() : null,
      });
      onDone();
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="remote-add-form" onSubmit={(e) => { void submit(e); }}>
      <input className="remote-input" value={displayName}
        onChange={(e) => setDisplayName(e.target.value)} aria-label="Display name" placeholder="display name" />
      <input className="remote-input" value={address}
        onChange={(e) => setAddress(e.target.value)} aria-label="Remote address" placeholder="host:port" />
      <input className="remote-input" type="password" value={token}
        onChange={(e) => setToken(e.target.value)} aria-label="Replace token" placeholder="replace token (leave blank to keep)" />
      <label className="remote-enabled-toggle">
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> Enabled
      </label>
      <div className="remote-row-actions">
        <button type="submit" className="remote-btn remote-btn-primary" disabled={busy}>Save</button>
        <button type="button" className="remote-btn" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  );
}
