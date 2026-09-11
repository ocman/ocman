import { usePageTitle } from '../lib/headerContext';
import { useSubscriptionUsage } from '../lib/queries';
import type { SubscriptionProviderUsage } from '../lib/api';

const STATUS_LABELS: Record<string, string> = {
  expired: 'OpenCode token expired',
  unauthorized: 'Authentication rejected',
  rate_limited: 'Temporarily rate limited',
  upstream_error: 'Usage unavailable',
};

function ProviderCard({ provider }: { provider: SubscriptionProviderUsage }) {
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
                Resets <time dateTime={window.resetsAt}>{new Date(window.resetsAt).toLocaleString()}</time>
              </span>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

export function SubscriptionUsage() {
  usePageTitle('Usage');
  const usage = useSubscriptionUsage();

  return (
    <div className="subscription-page">
      <div className="subscription-page-heading">
        <div>
          <h1>Subscription usage</h1>
          <p>Current limits reported for OAuth subscriptions connected to OpenCode.</p>
        </div>
        <button type="button" onClick={() => void usage.refetch()} disabled={usage.isFetching}>Refresh</button>
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
          {usage.data.providers.map((provider) => <ProviderCard provider={provider} key={provider.id} />)}
        </div>
      )}
    </div>
  );
}
