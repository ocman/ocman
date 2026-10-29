import { useCallback, useEffect, useState } from 'react';
import { Button, ButtonGroup, SelectField, TextField } from './Control';
import { SettingDescription, SettingRow } from './SettingRow';
import { prWorkflowApi, type ForgeConnection } from '../lib/prWorkflowApi';
import type { RemoteType } from '../lib/upstreamApi';
import { useConfirm } from '../lib/useConfirm';
import { useUnsavedChangesGuard } from '../lib/useUnsavedChangesGuard';
import './SourceControlSettings.css';

export function SourceControlSettings() {
  const [connections, setConnections] = useState<ForgeConnection[] | null>(null);
  const [provider, setProvider] = useState<RemoteType>('github');
  const [host, setHost] = useState('github.com');
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');
  const confirm = useConfirm();
  useUnsavedChangesGuard(!!token);
  const load = useCallback(async () => {
    const result = await prWorkflowApi.connections();
    setConnections(result.connections.sort((a, b) => a.host.localeCompare(b.host)));
  }, []);
  useEffect(() => { let active = true; void prWorkflowApi.connections().then((result) => { if (active) setConnections(result.connections); }).catch(() => { if (active) setError('Could not load source-control connections.'); }); return () => { active = false; }; }, []);
  const run = async (action: () => Promise<unknown>, message: string) => {
    setBusy(true); setError(''); setStatus('');
    try { await action(); setStatus(message); await load(); }
    catch (err) { setError(err instanceof Error ? err.message : 'Could not update source control.'); }
    finally { setBusy(false); }
  };
  return <div className="oc-source-control">
    <SettingDescription>Forge API requests use this hub's credentials, even for remote checkouts. Git fetch and push use the checkout machine's separate Git authentication.</SettingDescription>
    <Button disabled={busy} onClick={() => void run(prWorkflowApi.recheck, 'Credentials rechecked.')}>{busy ? 'Checking…' : 'Recheck credentials'}</Button>
    {error && <p role="alert">{error} <Button variant="ghost" disabled={busy} onClick={() => void run(load, '')}>Retry</Button></p>}
    {connections === null && !error && <p role="status">Loading connections…</p>}
    {connections?.length === 0 && <p>No forge credentials found. Sign in with <code>gh auth login</code> or <code>tea login add</code> on the hub, then recheck, or save a token below.</p>}
    {connections?.map((connection) => <SettingRow key={connection.host} label={connection.host} desc={connection.help} detail={<span>{connection.status}{connection.account ? ` as ${connection.account}` : ''} · {connection.source}</span>}>
      {connection.source === 'saved token' && <Button variant="danger" size="small" disabled={busy} onClick={() => void (async () => {
        if (await confirm({ title: 'Remove saved token?', description: `Requests to ${connection.host} will fall back to the hub's environment or CLI credentials.`, confirmLabel: 'Remove token', destructive: true })) await run(() => prWorkflowApi.saveCredential(connection.host, connection.provider, ''), 'Saved token removed.');
      })()}>Remove token</Button>}
    </SettingRow>)}
    <form onSubmit={(event) => { event.preventDefault(); void run(async () => { await prWorkflowApi.saveCredential(host, provider, token); setToken(''); }, 'Token verified and saved.'); }}>
      <SettingRow label="Provider" desc="Saved tokens override environment and CLI credentials for this host."><SelectField aria-label="Forge provider" value={provider} disabled={busy} onChange={(event) => { const value = event.target.value as RemoteType; setProvider(value); setHost(value === 'github' ? 'github.com' : ''); }}><option value="github">GitHub</option><option value="forgejo">Forgejo / Gitea</option></SelectField></SettingRow>
      <SettingRow label="Host" desc="Enter the hostname only, without https:// or a repository path."><TextField aria-label="Forge host" value={host} disabled={busy || provider === 'github'} required onChange={(event) => setHost(event.target.value)} placeholder="code.example.com" autoComplete="off" /></SettingRow>
      <SettingRow setting="source-control-token" desc="Use repository and pull-request read access. The token is verified before saving, stored in the hub's private state database, and never returned to the browser."><TextField aria-label="Forge access token" type="password" value={token} disabled={busy} required onChange={(event) => setToken(event.target.value)} autoComplete="new-password" /></SettingRow>
      <ButtonGroup label="Save source-control credentials"><Button type="submit" variant="accent" disabled={busy || !host.trim() || !token.trim()}>{busy ? 'Verifying…' : 'Verify and save token'}</Button><Button type="button" variant="ghost" disabled={busy || !token} onClick={() => setToken('')}>Cancel</Button></ButtonGroup>
    </form>
    {status && <p role="status">{status}</p>}
  </div>;
}
