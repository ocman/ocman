import { useQuery } from '@tanstack/react-query';
import { api, type ProviderAccount } from '../../lib/api';
import { timeUntilISO } from '../../lib/format';
import { SettingRow } from '../../components/SettingRow';
import { ChartCard } from './shared';
import { InlineAlert } from '../../components/InlineAlert';
import { LoadingState } from '../../components/LoadingState';
import { RefreshButton } from '../../components/RefreshButton';
import { useProviderUsageHistory } from '../../lib/providerUsageHistory';
import { subscriptionHistorySeries } from '../../lib/subscriptionHistorySeries';
import { ProviderUsageChart } from '../ProviderUsageChart';

const WINDOW_LABELS: Record<string, string> = { five_hour: '5-hour', weekly: 'Weekly' };
const STATUS: Record<ProviderAccount['status'], string> = {
  ok: '',
  signed_out: 'Not signed in.',
  unavailable: 'Account unavailable.',
};

function plan(type: string) {
  return type ? type.charAt(0).toUpperCase() + type.slice(1) : 'Unknown plan';
}

/** The 5-hour and weekly windows always show; one the platform has not reported yet says so. */
function windowNames(account: ProviderAccount) {
  return [...new Set([...Object.keys(WINDOW_LABELS), ...account.rateLimits.map((l) => l.name)])];
}

function useProviderAccounts() {
  return useQuery({
    queryKey: ['providerAccounts'],
    queryFn: ({ signal }) => api.providerAccounts(signal),
    staleTime: 60_000,
  });
}

/** Observed utilisation over time beside analytics; account settings keep the current readings. */
export function ProviderLimitsCard() {
  const query = useProviderAccounts();
  const history = useProviderUsageHistory(7);
  const groups = history.data ? subscriptionHistorySeries(history.data) : [];
  if (!query.data?.accounts.length) return null;
  return (
    <div className="subscription-limits-history">
      <ChartCard title="Subscription limits">
        <div className="subscription-page-heading">
          <p>Last 7 days. Gaps indicate missing readings or quota resets.</p>
          <RefreshButton size="small" onClick={() => void history.refetch()} loading={history.isFetching} />
        </div>
        {history.error instanceof Error && <InlineAlert onRetry={() => void history.refetch()} retrying={history.isFetching}>{history.error.message}</InlineAlert>}
        {history.isLoading && !history.data && <LoadingState>Loading subscription history</LoadingState>}
        {history.data && groups.length === 0 && !history.error && <p role="status">No usage history yet. Graphs start with the next successful platform reading.</p>}
        <div className="subscription-grid">
          {groups.map(group => <section key={group.id} aria-label={`${group.name} subscription limits`}>
            <h4>{group.name}</h4>
            <ProviderUsageChart name={group.name} series={group.series} />
          </section>)}
        </div>
      </ChartCard>
    </div>
  );
}

/** Subscription type and limit utilisation per account-capable platform (FR-7). */
export function ProviderAccountSettings() {
  const query = useProviderAccounts();
  if (query.isLoading) return <p role="status">Loading…</p>;
  if (query.error) return <p role="alert">{query.error instanceof Error ? query.error.message : String(query.error)}</p>;
  const accounts = query.data?.accounts ?? [];
  if (accounts.length === 0) return <p role="status">No enabled platform reports a subscription account.</p>;
  return (
    <>
      {accounts.map((account) => (
        <SettingRow
          key={account.platform}
          label={account.displayName}
          desc={account.status === 'ok' ? `Subscription: ${plan(account.subscriptionType)}` : STATUS[account.status]}
        >
          <div>
            {account.status === 'ok' && windowNames(account).map((name) => {
              const label = WINDOW_LABELS[name] ?? name;
              const limit = account.rateLimits.find((l) => l.name === name);
              if (!limit) return <div key={name}>{label}: not reported yet</div>;
              const percent = Math.round(limit.utilization * 100);
              const resets = limit.resetsAt > 0 ? timeUntilISO(new Date(limit.resetsAt).toISOString()) : '';
              return (
                <div key={name}>
                  <span>{label}: {percent}% used{resets && ` · resets in ${resets}`}</span>
                  <progress aria-label={`${account.displayName} ${label} utilisation`} max={100} value={percent} />
                </div>
              );
            })}
          </div>
        </SettingRow>
      ))}
    </>
  );
}
