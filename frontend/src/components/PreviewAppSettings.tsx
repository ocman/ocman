import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { loadPreviewApps, removePreviewApp, savePreviewApp } from '../lib/previews';
import type { PreviewApp, PreviewAppKind, PreviewApps } from '../lib/previews';
import { Button, SelectField, TextField } from './Control';
import { CopyButton } from './CopyButton';
import { SettingRow } from './SettingRow';

const appName = (kind: PreviewAppKind | undefined, app: { kind: string; host?: string }) =>
  (kind?.name ?? app.kind) + (app.host ? ` (${app.host})` : '');

const envHint = (k: PreviewAppKind) => k.hosted
  ? `${k.env}=host=client_id${k.secretOptional ? '[:secret]' : ':secret'}`
  : `${k.env}_CLIENT_ID / ${k.env}_CLIENT_SECRET`;

type Draft = { kind: string; host: string; clientId: string; clientSecret: string; editing: boolean };

/**
 * Sign-in (OAuth) apps that let viewers connect their own provider accounts.
 * Saved apps override the environment; secrets are write-only.
 */
export function PreviewAppSettings() {
  const [data, setData] = useState<PreviewApps | null>(null);
  const [draft, setDraft] = useState<Draft>({ kind: 'github', host: '', clientId: '', clientSecret: '', editing: false });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => { loadPreviewApps().then(setData).catch((err: unknown) => setError(String(err))); }, []);

  const run = (action: () => Promise<PreviewApps>, after?: () => void) => {
    setBusy(true);
    setError('');
    action().then((d) => { setData(d); after?.(); })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false));
  };

  if (!data) return error ? <p role="alert">{error}</p> : <p className="settings-row-desc" role="status">Loading sign-in apps…</p>;

  const kindOf = (kind: string) => data.kinds.find((k) => k.kind === kind);
  const kind = kindOf(draft.kind);
  const reset = () => setDraft({ kind: draft.kind, host: '', clientId: '', clientSecret: '', editing: false });
  const edit = (a: PreviewApp) => setDraft({ kind: a.kind, host: a.host ?? '', clientId: a.clientId, clientSecret: '', editing: true });
  const keepsSecret = draft.editing && data.apps.some((a) => a.kind === draft.kind && (a.host ?? '') === draft.host.trim().toLowerCase() && a.source === 'settings' && a.hasSecret);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    run(() => savePreviewApp({ kind: draft.kind, host: kind?.hosted ? draft.host : undefined, clientId: draft.clientId, clientSecret: draft.clientSecret }), reset);
  };

  return <>
    <SettingRow label="Redirect URI" desc="Register this exact URL with every provider app.">
      <code>{data.callbackUrl}</code>
      <CopyButton text={data.callbackUrl} label="Copy redirect URI" iconOnly />
    </SettingRow>
    {data.apps.map((a) => {
      const k = kindOf(a.kind);
      const source = a.source === 'env' ? 'From the environment' : a.inEnv ? 'Saved here, overriding the environment' : 'Saved here';
      return <SettingRow key={a.id} label={`${appName(k, a)} sign-in app`} desc={`${source} · client ID ${a.clientId}`}>
        <Button type="button" disabled={busy} aria-label={`${a.source === 'env' ? 'Override' : 'Edit'} ${appName(k, a)}`} onClick={() => edit(a)}>
          {a.source === 'env' ? 'Override' : 'Edit'}
        </Button>
        {a.source === 'settings' && <Button type="button" variant="muted" disabled={busy} aria-label={`Remove ${appName(k, a)}`}
          onClick={() => run(() => removePreviewApp(a.id))}>{a.inEnv ? 'Use environment' : 'Remove'}</Button>}
      </SettingRow>;
    })}
    <form onSubmit={submit} aria-label="Sign-in app">
      <SettingRow block label={draft.editing ? `Edit ${appName(kind, draft)}` : 'Add a sign-in app'}
        desc={kind && <>Or set <code>{envHint(kind)}</code>.</>}>
        <SelectField aria-label="Provider" value={draft.kind} disabled={draft.editing}
          onChange={(e) => setDraft({ ...draft, kind: e.target.value })}>
          {data.kinds.map((k) => <option key={k.kind} value={k.kind}>{k.name}</option>)}
        </SelectField>
        {kind?.hosted && <TextField aria-label="Host" placeholder="host[:port]" value={draft.host} disabled={draft.editing}
          onChange={(e) => setDraft({ ...draft, host: e.target.value })} />}
        <TextField aria-label="Client ID" placeholder="Client ID" value={draft.clientId} autoComplete="off"
          onChange={(e) => setDraft({ ...draft, clientId: e.target.value })} />
        <TextField aria-label="Client secret" type="password" autoComplete="new-password" value={draft.clientSecret}
          placeholder={keepsSecret ? 'Client secret (leave blank to keep)' : kind?.secretOptional ? 'Client secret (optional)' : 'Client secret'}
          onChange={(e) => setDraft({ ...draft, clientSecret: e.target.value })} />
        <Button type="submit" variant="accent" disabled={busy || !draft.clientId.trim() || (kind?.hosted && !draft.host.trim())}>Save</Button>
        {draft.editing && <Button type="button" variant="ghost" onClick={reset}>Cancel</Button>}
      </SettingRow>
    </form>
    {error && <p role="alert">{error}</p>}
  </>;
}
