import { useQuery } from '@tanstack/react-query';
import { api, type ProviderAccount } from '../../lib/api';
import { timeUntilISO } from '../../lib/format';
import { SettingRow } from '../../components/SettingRow';
import { ChartCard } from './shared';

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

/** Limit utilisation beside the usage views (AD-12); nothing when no platform reports an account. */
export function ProviderLimitsCard() {
  const query = useProviderAccounts();
  if (!query.data?.accounts.length) return null;
  return (
    <ChartCard title="Subscription limits">
      <ProviderAccountSettings />
    </ChartCard>
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
