import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '../components/Control';
import { EmptyState } from '../components/EmptyState';
import { RefreshButton } from '../components/RefreshButton';
import { api } from '../lib/api';
import type { WebhookDelivery } from '../lib/api.types';
import { formatDateTimeShort } from '../lib/format';
import { deliveryHint, outcomes } from '../lib/webhookFilters';

function pretty(json: string) {
  try { return JSON.stringify(JSON.parse(json), null, 2); } catch { return json; }
}

export function WebhookDeliveryLog({ inboxId, routineName }: { inboxId: string; routineName: (id: string) => string }) {
  const [deliveries, setDeliveries] = useState<WebhookDelivery[]>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try { setDeliveries(await api.webhookInboxes.deliveries(inboxId)); } catch (err) { setError(err instanceof Error ? err.message : 'Could not load deliveries.'); } finally { setLoading(false); }
  }, [inboxId]);
  useEffect(() => { void load(); }, [load]);
  const redeliver = async (deliveryId: string) => {
    if (!window.confirm('Redeliver this webhook? Every matching routine runs again.')) return;
    setLoading(true);
    setError('');
    try {
      await api.webhookInboxes.redeliver(inboxId, deliveryId);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not redeliver.');
      setLoading(false);
      return;
    }
    await load();
  };

  return (
    <section className="webhook-deliveries" aria-labelledby="webhook-deliveries-heading">
      <header><h3 id="webhook-deliveries-heading">Recent deliveries</h3><RefreshButton loading={loading} label="Refresh deliveries" onClick={() => void load()} /></header>
      {error && <p role="alert" className="routine-error">{error}</p>}
      {deliveries?.length === 0 && <EmptyState>No deliveries received yet.</EmptyState>}
      {deliveries?.map((d) => {
        const hint = deliveryHint(d);
        const errors = d.dispatches.filter((x) => x.error).map((x) => `${routineName(x.routineId)}: ${x.error}`);
        if (d.lastError) errors.unshift(`${d.lastError} (attempt ${d.attempts})`);
        return (
          <details key={d.deliveryId} className="webhook-delivery">
            <summary>
              <span>{formatDateTimeShort(d.acceptedAt)}</span>
              {hint && <code>{hint}</code>}
              <span className="webhook-delivery-outcomes">{outcomes(d, routineName).map((o) => o.href
                ? <Link key={o.label} to={o.href} className={`routine-state ${o.tone}`} title="Open session" onClick={(e) => e.stopPropagation()}>{o.label} <i className="bi bi-box-arrow-up-right" aria-hidden="true" /></Link>
                : <span key={o.label} className={`routine-state ${o.tone}`}>{o.label}</span>)}</span>
            </summary>
            {errors.map((message) => <p key={message} className="routine-error">{message}</p>)}
            {d.accepted && <div className="routine-actions"><Button type="button" size="small" disabled={loading} onClick={() => void redeliver(d.deliveryId)}>Redeliver</Button></div>}
            <h4>Headers</h4>
            <pre>{pretty(d.headers)}</pre>
            <h4>Body</h4>
            <pre>{d.accepted ? pretty(d.body) || '(empty)' : '(not decrypted)'}</pre>
          </details>
        );
      })}
    </section>
  );
}
