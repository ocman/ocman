import { useState } from 'react';
import { Button, TextField } from '../components/Control';
import { api } from '../lib/api';
import type { WebhookInbox } from '../lib/api.types';

type Props = { inbox: WebhookInbox; run: (action: () => Promise<void>) => void; busy: boolean; onChange: () => void };

/** Rename an inbox or replace/remove its shared secret without changing its URL. */
export function WebhookInboxSettings({ inbox, run, busy, onChange }: Props) {
  const [name, setName] = useState(inbox.name);
  const [secret, setSecret] = useState('');
  const [secretHeader, setSecretHeader] = useState(inbox.secretHeader || 'Authorization');
  const [saved, setSaved] = useState('');

  const save = (input: Parameters<typeof api.webhookInboxes.update>[1], message: string) => run(async () => {
    setSaved('');
    await api.webhookInboxes.update(inbox.id, input);
    setSecret('');
    setSaved(message);
    onChange();
  });

  return (
    <section aria-labelledby="webhook-settings-heading" className="webhook-settings">
      <h3 id="webhook-settings-heading">Settings</h3>
      <div className="webhook-settings-row">
        <label>Name<TextField value={name} disabled={busy} onChange={(e) => setName(e.target.value)} /></label>
        <Button type="button" disabled={busy || !name.trim() || name.trim() === inbox.name} onClick={() => save({ name }, 'Name saved.')}>Rename</Button>
      </div>
      <p>{inbox.secretHeader ? <>Deliveries must send the shared secret in <code>{inbox.secretHeader}</code>.</> : 'No shared secret recorded: either none is set, or the inbox predates this record. Set one below to be sure.'}</p>
      <div className="webhook-settings-row">
        <label>Secret header<TextField value={secretHeader} disabled={busy} onChange={(e) => setSecretHeader(e.target.value)} /></label>
        <label>New shared secret<TextField type="password" autoComplete="off" value={secret} disabled={busy} onChange={(e) => setSecret(e.target.value)} /></label>
      </div>
      <small>The value must match exactly what the provider sends, e.g. <code>Bearer …</code> for Forgejo&apos;s Authorization header. The ingestion URL stays the same.</small>
      <div className="routine-actions">
        <Button type="button" disabled={busy || !secret || !secretHeader.trim()} onClick={() => save({ secret, secretHeader }, 'Secret updated.')}>Update secret</Button>
        <Button type="button" variant="ghost" disabled={busy} onClick={() => { if (window.confirm('Remove the shared secret? Anyone with the URL can then deliver.')) save({ secret: '' }, 'Secret removed.'); }}>Remove secret</Button>
      </div>
      {saved && <p role="status">{saved}</p>}
    </section>
  );
}
