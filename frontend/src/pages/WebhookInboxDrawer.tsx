import { useEffect, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { Button, TextField } from '../components/Control';
import { CopyButton } from '../components/CopyButton';
import { DataTable } from '../components/DataTable';
import { EmptyState } from '../components/EmptyState';
import { Modal } from '../components/Modal';
import { ModalFooter } from '../components/ModalFooter';
import { ModalHeader } from '../components/ModalHeader';
import { api, type Routine } from '../lib/api';
import type { WebhookInbox, WebhookRelaySettings } from '../lib/api.types';
import { WebhookDeliveryLog } from './WebhookDeliveryLog';
import { WebhookInboxSettings } from './WebhookInboxSettings';
import { describeFilters } from '../lib/webhookFilters';

type Props = {
  inbox: WebhookInbox | null;
  routines: Routine[];
  onClose: () => void;
  onChange: () => void;
  onEditRoutine: (routine: Routine) => void;
};

/** Create an inbox, or inspect one: URL, keys, subscribed routines and recent deliveries. */
export function WebhookInboxDrawer({ inbox, routines, onClose, onChange, onEditRoutine }: Props) {
  const [name, setName] = useState('');
  const [enrollmentToken, setEnrollmentToken] = useState('');
  const [secret, setSecret] = useState('');
  const [secretHeader, setSecretHeader] = useState('Authorization');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [relay, setRelay] = useState<WebhookRelaySettings>();
  useEffect(() => {
    if (!inbox) api.getWebhookRelay().then(setRelay, () => setRelay(undefined));
  }, [inbox]);
  const storedToken = relay?.hasEnrollmentToken ?? false;
  const routineName = (id: string) => routines.find((r) => r.id === id)?.name ?? id;

  const run = async (action: () => Promise<void>) => {
    setBusy(true);
    setError('');
    try { await action(); } catch (err) { setError(err instanceof Error ? err.message : 'Webhook action failed.'); } finally { setBusy(false); }
  };

  const create = (event: FormEvent) => {
    event.preventDefault();
    void run(async () => {
      await api.webhookInboxes.create({ name, enrollmentToken, secret, secretHeader: secret ? secretHeader : '' });
      onChange();
      onClose();
    });
  };

  const title = inbox ? `Inbox: ${inbox.name || inbox.id}` : 'New webhook inbox';
  const ingestionUrl = inbox ? new URL(inbox.ingestionUrl, inbox.relayUrl).href : '';
  const subscribers = inbox?.subscriptions.flatMap((sub) => { const routine = routines.find((r) => r.id === sub.routineId); return routine ? [{ routine, sub }] : []; }) ?? [];
  return (
    <Modal label={title} onClose={onClose} canClose={!busy} backdropClassName="routine-drawer-backdrop" dialogClassName="routine-drawer" backdropTestId="webhook-drawer-backdrop">
      <form className="routine-form" onSubmit={create}>
        <ModalHeader title={title} canClose={!busy} onClose={onClose} closeLabel="Close webhook drawer" />
        {error && <p role="alert" className="routine-error">{error}</p>}
        {inbox ? <>
          <label>Ingestion URL<TextField readOnly value={ingestionUrl} onFocus={(event) => event.currentTarget.select()} /></label>
          <p className="routine-webhook-status">Key v{inbox.keyVersion} · {Object.entries(inbox.counts).map(([state, count]) => `${state}: ${count}`).join(' · ') || 'no deliveries yet'}</p>
          <div className="routine-actions">
            <CopyButton disabled={busy} label="Copy URL" text={ingestionUrl} />
            <Button type="button" disabled={busy} onClick={() => { if (window.confirm('Reset the key? Existing pending deliveries will become unreadable.')) void run(async () => { await api.webhookInboxes.rotate(inbox.id, { reset: true }); onChange(); }); }}>Reset key</Button>
            <Button type="button" variant="danger" disabled={busy} onClick={() => { if (window.confirm('Revoke this webhook inbox? Linked routines stop receiving its deliveries.')) void run(async () => { await api.webhookInboxes.revoke(inbox.id); onChange(); onClose(); }); }}>Revoke</Button>
          </div>
          <WebhookInboxSettings key={`${inbox.name}:${inbox.secretHeader}`} inbox={inbox} run={(action) => void run(action)} busy={busy} onChange={onChange} />
          <section aria-labelledby="webhook-subscribers-heading" className="webhook-subscribers">
            <h3 id="webhook-subscribers-heading">Linked routines</h3>
            {subscribers.length === 0 ? <EmptyState>No routine uses this inbox yet. Pick it as the Trigger of a routine.</EmptyState> : (
              <DataTable framed aria-label="Linked routines"><thead><tr><th>Routine</th><th>Runs on</th><th>Status</th></tr></thead><tbody>
                {subscribers.map(({ routine, sub }) => (
                  <tr key={routine.id}>
                    <td><Button type="button" variant="ghost" size="small" onClick={() => onEditRoutine(routine)}>{routine.name}</Button></td>
                    <td><code>{describeFilters(sub.headerPredicates, sub.jsonPredicates)}</code></td>
                    <td><span className={`routine-state ${routine.enabled ? '' : 'disabled'}`}>{routine.enabled ? 'enabled' : 'disabled'}</span></td>
                  </tr>
                ))}
              </tbody></DataTable>
            )}
          </section>
          <WebhookDeliveryLog inboxId={inbox.id} routineName={routineName} />
        </> : <>
          <p>An inbox captures deliveries from a provider. Routines subscribe to it and filter which deliveries run them.</p>
          <label>Name<TextField required value={name} placeholder="forgejo" onChange={(e) => setName(e.target.value)} /></label>
          <p>Relay: <code>{relay?.relayUrl || relay?.defaultRelayUrl || 'not configured'}</code>. {storedToken ? 'Using the enrollment token from' : 'Save the relay and enrollment token once in'} <Link to="/settings">Settings → Webhooks</Link>.</p>
          {!storedToken && <label>Relay enrollment token<TextField required type="password" autoComplete="off" value={enrollmentToken} onChange={(e) => setEnrollmentToken(e.target.value)} /></label>}
          <label>Shared secret<TextField type="password" autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} /><small>Optional. The relay rejects requests whose header doesn&apos;t carry this exact value. Leave blank to rely on the URL alone.</small></label>
          {secret && <label>Secret header<TextField required value={secretHeader} onChange={(e) => setSecretHeader(e.target.value)} /><small>For Forgejo, keep Authorization and use a &quot;Bearer …&quot; secret. Use another header name for providers that send one.</small></label>}
          <ModalFooter label="Webhook inbox actions"><Button type="submit" variant="accent" disabled={busy || !name.trim() || (!storedToken && !enrollmentToken)}>Create inbox</Button><Button type="button" disabled={busy} onClick={onClose}>Cancel</Button></ModalFooter>
        </>}
      </form>
    </Modal>
  );
}
