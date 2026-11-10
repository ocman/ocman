import { useQuery } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { ChartCard, ChartSkeleton } from './shared';
import { InlineAlert } from '../../components/InlineAlert';
import { RefreshButton } from '../../components/RefreshButton';
import { useProviderUsageHistory } from '../../lib/providerUsageHistory';
import { subscriptionHistorySeries } from '../../lib/subscriptionHistorySeries';
import { ProviderUsageChart } from '../ProviderUsageChart';

function useProviderAccounts() {
  return useQuery({
    queryKey: ['providerAccounts'],
    queryFn: ({ signal }) => api.providerAccounts(signal),
    staleTime: 60_000,
  });
}

/** Observed utilisation over time in analytics; Provider usage keeps the current readings. */
export function ProviderLimitsCard({ days = 7 }: { days?: number }) {
  const query = useProviderAccounts();
  const history = useProviderUsageHistory(days);
  const groups = history.data ? subscriptionHistorySeries(history.data) : [];
  if (query.isLoading && !query.data) return <ChartSkeleton label="Loading subscription history" />;
  if (!query.data?.accounts.length) return null;
  if (history.isLoading && !history.data) return <ChartSkeleton label="Loading subscription history" />;
  return (
    <div className="subscription-limits-history">
      <ChartCard title="Subscription burndown">
        <div className="subscription-page-heading">
          <p>{days === 0 ? 'All history' : days === 1 ? 'Last 24 hours' : `Last ${days} days`}. Gaps indicate missing readings or quota resets.</p>
          <RefreshButton size="small" onClick={() => void history.refetch()} loading={history.isFetching} />
        </div>
        {history.error instanceof Error && <InlineAlert onRetry={() => void history.refetch()} retrying={history.isFetching}>{history.error.message}</InlineAlert>}
        {history.data && groups.length === 0 && !history.error && <p role="status">No usage history yet. Graphs start with the next successful platform reading.</p>}
        <div className="subscription-grid">
          {groups.map(group => <section key={group.id} aria-label={`${group.name} subscription burndown`}>
            <h4>{group.name}</h4>
            <ProviderUsageChart name={group.name} series={group.series} days={days} updatedAt={history.dataUpdatedAt} />
          </section>)}
        </div>
      </ChartCard>
    </div>
  );
}
