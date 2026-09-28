import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import {
  PREVIEW_AUTH_EVENT, connectPreviewProvider, disconnectPreviewProvider, loadPreviewConfig, savePreviewToken,
} from '../lib/previews';
import type { PreviewConfig, PreviewConnection, PreviewProvider } from '../lib/previews';
import { Button, SelectField, TextField } from './Control';
import { SettingRow } from './SettingRow';

const account = (c: PreviewConnection) => {
  const where = [c.workspaceName || c.workspaceId, ...c.sites.map((s) => s.name)].filter(Boolean).join(', ');
  const who = c.accountName && c.accountName !== where ? `${c.accountName} · ${where}` : where;
  return c.state === 'expired' ? `Expired: ${who}` : who;
};

function status(p: PreviewProvider): string {
  if (p.accounts.length) return 'Using a saved token or sign-in.';
  if (p.source === 'cli') return "Using this machine's CLI login (gh, tea or a *_TOKEN variable).";
  if (p.source === 'public') return 'Public resources only. Add a token for private ones.';
  return p.token ? 'Not set up. Links stay plain until you add a token.' : 'Not set up. Sign in to preview its links.';
}

/** Inline token entry: the token is checked with the provider, then sealed on the server. */
function TokenForm({ provider, label, help, onDone }: { provider: () => string; label: string; help?: string; onDone: () => void }) {
  const [token, setToken] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    savePreviewToken(provider(), token)
      .then(() => { setToken(''); onDone(); })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false));
  };
  return <form onSubmit={submit} aria-label={label} className="preview-token-form">
    {help && <p className="settings-row-desc">{help}</p>}
    <TextField aria-label={`${label} token`} type="password" autoComplete="off" placeholder="Token"
      value={token} onChange={(e) => setToken(e.target.value)} />
    <Button type="submit" variant="accent" disabled={busy || !token.trim()}>Save token</Button>
    {error && <p role="alert">{error}</p>}
  </form>;
}

function ProviderRow({ p, busy, run }: { p: PreviewProvider; busy: boolean; run: (a: () => Promise<void>) => void }) {
  const [editing, setEditing] = useState(false);
  return <SettingRow
    block={editing}
    label={p.name}
    desc={<>
      <div>{status(p)}</div>
      {p.accounts.map((c) => <div key={c.workspaceId}>{account(c)}</div>)}
      {editing && <TokenForm provider={() => p.id} label={p.name} help={p.tokenHelp} onDone={() => setEditing(false)} />}
    </>}
  >
    {p.token && !editing && <Button type="button" disabled={busy} aria-label={`Add token for ${p.name}`} onClick={() => setEditing(true)}>
      {p.accounts.length ? 'Add token' : 'Set token'}
    </Button>}
    {editing && <Button type="button" variant="ghost" onClick={() => setEditing(false)}>Cancel</Button>}
    {p.oauth && <Button type="button" disabled={busy} aria-label={`Sign in to ${p.name}`} onClick={() => run(() => connectPreviewProvider(p.id))}>Sign in</Button>}
    {p.accounts.map((c) => (
      <Button key={c.workspaceId} type="button" variant="muted" disabled={busy}
        aria-label={`Remove ${p.name} ${c.workspaceName || c.workspaceId}`}
        onClick={() => run(() => disconnectPreviewProvider(p.id, c.workspaceId))}>Remove</Button>
    ))}
  </SettingRow>;
}

/**
 * The providers link previews support on this machine, how each is set up,
 * and forms to paste a personal token. Tokens stay on the server.
 */
export function PreviewProviderSettings() {
  const [config, setConfig] = useState<PreviewConfig | null>(null);
  const [error, setError] = useState(() =>
    new URLSearchParams(window.location.search).get('previewAuth') === 'error' ? 'Signing in to the provider failed. Try again.' : '');
  const [busy, setBusy] = useState(false);
  const [hostKind, setHostKind] = useState('forgejo');
  const [host, setHost] = useState('');

  useEffect(() => {
    let active = true;
    const load = () => {
      loadPreviewConfig().then((c) => { if (active) setConfig(c); })
        .catch((err: unknown) => { if (active) setError(String(err)); });
    };
    load();
    window.addEventListener(PREVIEW_AUTH_EVENT, load);
    return () => { active = false; window.removeEventListener(PREVIEW_AUTH_EVENT, load); };
  }, []);

  const run = (action: () => Promise<void>) => {
    setBusy(true);
    setError('');
    action().catch((err: unknown) => setError(err instanceof Error ? err.message : String(err))).finally(() => setBusy(false));
  };

  if (config === null) return error ? <p role="alert">{error}</p> : <p className="settings-row-desc" role="status">Loading providers…</p>;
  const kind = config.hostKinds.find((k) => k.kind === hostKind);
  return <>
    {config.providers.map((p) => <ProviderRow key={p.id} p={p} busy={busy} run={run} />)}
    {config.hostKinds.length > 0 && <SettingRow block label="Add a host" desc="Preview a self-hosted Forgejo or GitLab with a personal token.">
      <SelectField aria-label="Host type" value={hostKind} onChange={(e) => setHostKind(e.target.value)}>
        {config.hostKinds.map((k) => <option key={k.kind} value={k.kind}>{k.name}</option>)}
      </SelectField>
      <TextField aria-label="Host" placeholder="host[:port]" value={host} onChange={(e) => setHost(e.target.value)} />
      {host.trim() && <TokenForm provider={() => `${hostKind}:${host.trim().toLowerCase()}`} label={`${kind?.name ?? hostKind} ${host.trim()}`}
        help={kind?.help} onDone={() => setHost('')} />}
    </SettingRow>}
    {error && <p role="alert">{error}</p>}
  </>;
}
