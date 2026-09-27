import { useEffect, useState } from 'react';
import {
  PREVIEW_AUTH_EVENT, connectPreviewProvider, disconnectPreviewProvider, loadPreviewProviders,
} from '../lib/previews';
import type { PreviewConnection, PreviewProvider } from '../lib/previews';
import { Button } from './Control';
import { SettingRow } from './SettingRow';

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
  const [error, setError] = useState(() =>
    new URLSearchParams(window.location.search).get('previewAuth') === 'error' ? 'Connecting the provider failed. Try again.' : '');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    const load = () => {
      loadPreviewProviders().then((p) => { if (active) setProviders(p); })
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
    {providers.length === 0 && <p className="settings-row-desc">No preview integrations are configured on this server.</p>}
    {providers.map((p) => {
      const expired = p.connections.some((c) => c.state === 'expired');
      const connectLabel = expired ? 'Reconnect' : p.connections.length ? 'Connect another workspace' : 'Connect';
      return <SettingRow
        key={p.id}
        label={p.name}
        desc={<>
          {p.connections.length ? p.connections.map((c) => <div key={c.workspaceId}>{describe(c)}</div>) : 'Not connected'}
          {p.notice && <div>{p.notice}</div>}
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
