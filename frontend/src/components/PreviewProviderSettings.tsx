import { useEffect, useState } from 'react';
import {
  PREVIEW_AUTH_EVENT, connectPreviewProvider, disconnectPreviewProvider, loadPreviewOwnerTokens, loadPreviewProviders,
} from '../lib/previews';
import type { PreviewConnection, PreviewOwnerToken, PreviewProvider } from '../lib/previews';
import { Button } from './Control';
import { SettingRow } from './SettingRow';

const OWNER_TOKEN_NOTE = "Uses this machine's token. Public repositories preview for everyone; private ones only in a browser on this machine.";

const describe = (c: PreviewConnection) => {
  const where = [c.workspaceName || c.workspaceId, ...c.sites.map((s) => s.name)].filter(Boolean).join(', ');
  const who = c.accountName ? `${c.accountName} · ${where}` : where;
  return c.state === 'expired' ? `Expired: ${who}` : `Connected: ${who}`;
};

/**
 * Settings rows for preview integrations. The browser only ever sees display
 * names; consent, tokens and refresh stay on the server.
 */
export function PreviewProviderSettings() {
  const [providers, setProviders] = useState<PreviewProvider[] | null>(null);
  const [owners, setOwners] = useState<PreviewOwnerToken[]>([]);
  const [error, setError] = useState(() =>
    new URLSearchParams(window.location.search).get('previewAuth') === 'error' ? 'Connecting the provider failed. Try again.' : '');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    const load = () => {
      Promise.all([loadPreviewProviders(), loadPreviewOwnerTokens()])
        .then(([p, o]) => { if (active) { setProviders(p); setOwners(o); } })
        .catch((err: unknown) => { if (active) setError(String(err)); });
    };
    load();
    window.addEventListener(PREVIEW_AUTH_EVENT, load);
    return () => { active = false; window.removeEventListener(PREVIEW_AUTH_EVENT, load); };
  }, []);

  const run = (action: () => Promise<void>) => {
    setBusy(true);
    setError('');
    action().catch((err: unknown) => setError(String(err))).finally(() => setBusy(false));
  };

  if (providers === null) return error ? <p role="alert">{error}</p> : <p className="settings-row-desc" role="status">Loading integrations…</p>;
  return <>
    {owners.filter((o) => !providers.some((p) => p.id === o.provider)).map((o) => (
      <SettingRow key={o.provider} label={`${o.name} (${o.host})`} desc={OWNER_TOKEN_NOTE}>{null}</SettingRow>
    ))}
    {providers.length === 0 && <p className="settings-row-desc">No sign-in apps yet. Add one under Sign-in apps so viewers can connect their own accounts.</p>}
    {providers.map((p) => {
      const expired = p.connections.some((c) => c.state === 'expired');
      const connectLabel = expired ? 'Reconnect' : p.connections.length ? 'Connect another workspace' : 'Connect';
      return <SettingRow
        key={p.id}
        label={p.name}
        desc={<>
          {p.connections.length ? p.connections.map((c) => <div key={c.workspaceId}>{describe(c)}</div>) : 'Not connected'}
          {p.notice && <div>{p.notice}</div>}
          {owners.some((o) => o.provider === p.id) && <div>Without a connection, this machine's token still previews public repositories.</div>}
        </>}
      >
        <Button type="button" disabled={busy} aria-label={`${connectLabel} ${p.name}`} onClick={() => run(() => connectPreviewProvider(p.id))}>{connectLabel}</Button>
        {p.connections.map((c) => (
          <Button key={c.workspaceId} type="button" variant="muted" disabled={busy}
            aria-label={`Disconnect ${p.name} ${c.workspaceName || c.workspaceId}`}
            onClick={() => run(() => disconnectPreviewProvider(p.id, c.workspaceId))}>Disconnect</Button>
        ))}
      </SettingRow>;
    })}
    {error && <p role="alert">{error}</p>}
  </>;
}
