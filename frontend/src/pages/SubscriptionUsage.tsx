import { usePageTitle } from '../lib/headerContext';
import { useSubscriptionUsage } from '../lib/queries';
import { formatSubscriptionResetDate, timeUntilISO } from '../lib/format';
import type { SubscriptionProviderUsage } from '../lib/api';
import { Button } from '../components/Control';

const STATUS_LABELS: Record<string, string> = {
  expired: 'OpenCode token expired',
  unauthorized: 'Authentication rejected',
  rate_limited: 'Temporarily rate limited',
  upstream_error: 'Usage unavailable',
};

function ProviderCard({ provider, compact = false }: { provider: SubscriptionProviderUsage; compact?: boolean }) {
  return (
    <section className="subscription-card" aria-labelledby={`subscription-${provider.id}`}>
      <header className="subscription-card-header">
        <h2 id={`subscription-${provider.id}`}>{provider.name}</h2>
        {provider.plan && <span className="subscription-plan">{provider.plan.charAt(0).toUpperCase() + provider.plan.slice(1)}</span>}
      </header>
      {provider.status !== 'ok' && <p className="subscription-status">{STATUS_LABELS[provider.status] ?? 'Usage unavailable'}</p>}
      {provider.status === 'ok' && provider.windows.length === 0 && <p className="subscription-status">No quota windows reported.</p>}
      <div className="subscription-windows">
        {provider.windows.map((window) => (
          <div className="subscription-window" key={`${window.name}-${window.resetsAt ?? ''}`}>
            <div className="subscription-window-label">
              <strong>{window.name}</strong>
              <span>{window.usedPercent}% used</span>
            </div>
            <progress
              aria-label={`${provider.name} ${window.name} usage`}
              max={100}
              value={window.usedPercent}
            />
            {window.resetsAt && (
              <span className="subscription-reset">
                {compact && timeUntilISO(window.resetsAt)
                  ? <>Resets in {timeUntilISO(window.resetsAt)}</>
                  : <>
                    Resets <time dateTime={window.resetsAt}>{formatSubscriptionResetDate(window.resetsAt)}</time>
                    {timeUntilISO(window.resetsAt) && <span className="subscription-reset-in"> (in {timeUntilISO(window.resetsAt)})</span>}
                  </>}
              </span>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

export function SubscriptionUsageContent({ compact = false }: { compact?: boolean }) {
  const usage = useSubscriptionUsage();

  return (
    <div className="subscription-content">
      <div className="subscription-page-heading">
        <div>
          {compact ? <h2>Subscription usage</h2> : <h1>Subscription usage</h1>}
          <p>Current limits reported for OAuth subscriptions connected to OpenCode.</p>
        </div>
        <Button size="small" onClick={() => void usage.refetch()} disabled={usage.isFetching}>Refresh</Button>
      </div>
      {usage.error instanceof Error && (
        <div className="oc-error-banner" role="alert">
          {usage.error.message}
          <button type="button" onClick={() => void usage.refetch()}>Retry</button>
        </div>
      )}
      {usage.isLoading && !usage.data && <div className="oc-list-loading" role="status"><span className="oc-spinner" />Loading subscription usage</div>}
      {usage.data?.providers.length === 0 && <div className="oc-empty">No OpenCode subscription credentials found.</div>}
      {usage.data && usage.data.providers.length > 0 && (
        <div className="subscription-grid">
          {usage.data.providers.map((provider) => <ProviderCard provider={provider} compact={compact} key={provider.id} />)}
        </div>
      )}
    </div>
  );
}

export function SubscriptionUsage() {
  usePageTitle('Usage');
  return <div className="subscription-page"><SubscriptionUsageContent /></div>;
}
