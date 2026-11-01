import { usePageTitle } from '../lib/headerContext';
import { useSubscriptionUsage } from '../lib/queries';
import { formatSubscriptionResetDate, timeUntilISO } from '../lib/format';
import type { SubscriptionProviderUsage } from '../lib/api';
import { RefreshButton } from '../components/RefreshButton';
import { InlineAlert } from '../components/InlineAlert';
import { EmptyState } from '../components/EmptyState';
import { LoadingState } from '../components/LoadingState';
import { ProviderSpend } from './ProviderSpend';
import { ProviderBudget } from './ProviderBudget';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '../components/Tabs';
import './SubscriptionUsage.css';

const History = lazy(() => import('./SubscriptionUsageHistory').then(module => ({ default: module.SubscriptionUsageHistory })));

const STATUS_LABELS: Record<string, string> = {
  expired: 'Platform authentication expired',
  signed_out: 'Sign in using the platform app to view usage.',
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
      {provider.status === 'ok' && provider.windows.length === 0 && !provider.spend && !provider.budgets?.length && <p className="subscription-status">No quota windows reported.</p>}
      {provider.spend && <ProviderSpend spend={provider.spend} />}
      <div className="subscription-windows">
        {provider.budgets?.map((budget) => <ProviderBudget key={budget.id ?? budget.name} budget={budget} />)}
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
          {compact ? <h2>Provider usage</h2> : <h1>Provider usage</h1>}
          <p>Subscription limits and budgets reported by enabled platform apps. Local API estimates are listed separately.</p>
        </div>
        <RefreshButton size="small" variant="default" onClick={() => void usage.refetch()} loading={usage.isFetching} />
      </div>
      {usage.error instanceof Error && (
        <InlineAlert onRetry={() => void usage.refetch()} retrying={usage.isFetching}>
          {usage.error.message}
        </InlineAlert>
      )}
      {usage.isLoading && !usage.data && <LoadingState>Loading provider usage</LoadingState>}
      {usage.data?.providers.length === 0 && <EmptyState>No enabled platform apps report provider usage.</EmptyState>}
      {usage.data && usage.data.providers.length > 0 && (
        <div className="subscription-grid">
          {usage.data.providers.map((provider) => <ProviderCard provider={provider} compact={compact} key={provider.id} />)}
        </div>
      )}
    </div>
  );
}

export function SubscriptionUsage() {
  usePageTitle('Provider usage');
  const [params, setParams] = useSearchParams();
  const view = params.get('view') === 'history' ? 'history' : 'current';
  return <div className="subscription-page"><Tabs value={view} onValueChange={value => setParams(previous => {
    const next = new URLSearchParams(previous);
    if (value === 'current') next.delete('view'); else next.set('view', value);
    return next;
  })}>
    <TabsList aria-label="Provider usage views"><TabsTrigger value="current">Current usage</TabsTrigger><TabsTrigger value="history">History</TabsTrigger></TabsList>
    <TabsContent value="current"><SubscriptionUsageContent /></TabsContent>
    <TabsContent value="history"><Suspense fallback={<LoadingState>Loading subscription history</LoadingState>}><History /></Suspense></TabsContent>
  </Tabs></div>;
}
import { lazy, Suspense } from 'react';
import { useSearchParams } from 'react-router-dom';
