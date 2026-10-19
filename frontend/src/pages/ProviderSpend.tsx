import type { ProviderSpend as Spend } from '../lib/api.providerUsage';

function formatEstimate(value: number): string {
  if (value > 0 && value < 0.000001) return '<$0.000001';
  return new Intl.NumberFormat('en-US', {
    style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 6,
  }).format(value);
}

export function ProviderSpend({ spend }: { spend: Spend }) {
  return (
    <div className="subscription-window">
      <div className="subscription-window-label">
        <strong>Estimated cost · last 7 days</strong>
        <span>{spend.estimatedUSD === null ? 'Unavailable' : formatEstimate(spend.estimatedUSD)}</span>
      </div>
      <span className="subscription-status">
        {spend.requests === 0
          ? 'No recorded requests yet.'
          : `${spend.requests.toLocaleString()} ${spend.requests === 1 ? 'request' : 'requests'} · ${spend.inputTokens.toLocaleString()} input tokens · ${spend.outputTokens.toLocaleString()} output tokens`}
      </span>
      {spend.unpricedRequests > 0 && (
        <span className="subscription-status">
          {spend.unpricedRequests} {spend.unpricedRequests === 1 ? 'request could' : 'requests could'} not be priced. Estimate excludes these requests.
        </span>
      )}
      <span className="subscription-status">
        Recorded by this ocman instance only, across configured TypeSafe keys. Not account-wide spend.
        {' '}Tracking starts after this update; earlier requests and other apps are not included.
      </span>
      <a href="https://console.typesafe.ai" target="_blank" rel="noreferrer">View account billing</a>
    </div>
  );
}
