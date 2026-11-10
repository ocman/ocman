import { useSearchParams } from 'react-router-dom';
import { ProviderUsageChart } from './ProviderUsageChart';
import { SelectField } from '../components/Control';
import { RefreshButton } from '../components/RefreshButton';
import { ChartSkeleton } from './dashboard/shared';
import { EmptyState } from '../components/EmptyState';
import { InlineAlert } from '../components/InlineAlert';
import { useProviderUsageHistory } from '../lib/providerUsageHistory';
import { subscriptionHistorySeries } from '../lib/subscriptionHistorySeries';

export function SubscriptionUsageHistory() {
  const [params, setParams] = useSearchParams();
  const raw = Number(params.get('days') || '7');
  const days = [0, 1, 7, 30, 90, 365].includes(raw) ? raw : 7;
  const history = useProviderUsageHistory(days);
  const groups = history.data ? subscriptionHistorySeries(history.data) : [];
  return <div className="subscription-content">
    <div className="subscription-page-heading">
      <div><h1>Subscription burndown</h1><p>Five-minute readings for 30 days, then the latest snapshot per hour kept indefinitely. Gaps indicate missing readings or quota resets.</p></div>
      <label>Period <SelectField aria-label="History period" value={days} onChange={event => setParams(previous => {
        const next = new URLSearchParams(previous);
        next.set('days', event.target.value);
        return next;
      })}>
        <option value="1">Last 24 hours</option><option value="7">Last 7 days</option><option value="30">Last 30 days</option>
        <option value="90">Last 90 days</option><option value="365">Last year</option><option value="0">All history</option>
      </SelectField></label>
      <RefreshButton size="small" onClick={() => void history.refetch()} loading={history.isFetching} />
    </div>
    {history.error instanceof Error && <InlineAlert onRetry={() => void history.refetch()} retrying={history.isFetching}>{history.error.message}</InlineAlert>}
    {history.isLoading && !history.data && <ChartSkeleton label="Loading subscription history" />}
    {history.data && groups.length === 0 && <EmptyState>No quota windows or budgets recorded yet. History starts with the next successful platform reading.</EmptyState>}
    {groups.length > 0 && <div className="subscription-grid">
      {groups.map(group => <section key={group.id} className="subscription-card" aria-labelledby={`history-${group.id}`}>
        <header className="subscription-card-header"><h2 id={`history-${group.id}`}>{group.name}</h2></header>
        <ProviderUsageChart name={group.name} series={group.series} days={days} updatedAt={history.dataUpdatedAt} />
      </section>)}
    </div>}
  </div>;
}
