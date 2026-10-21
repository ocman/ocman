import { useQuery } from '@tanstack/react-query';
import { fetchJSON } from '../lib/api.requests';
import { withDeadline } from '../lib/coalescedRefresh';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { useBackendStatus } from '../lib/backendStatus';
import { useStartupStatus } from '../lib/startupStatus';
import { RouteButton } from './Control';
import { SettingRow } from './SettingRow';

interface HistoryHealth {
  status: 'ready' | 'syncing' | 'unavailable';
  sources: number;
  lastCheckedAt?: string;
}

export function ApplicationHealthSettings() {
  const visible = useDocumentVisible();
  const backend = useBackendStatus();
  const startup = useStartupStatus();
  const query = useQuery({
    queryKey: ['application-health', 'history'],
    queryFn: ({ signal }) => withDeadline(10_000, (deadline) => fetchJSON<HistoryHealth>('/api/analytics/history-health', deadline), signal),
    enabled: visible,
    refetchInterval: 5_000,
    refetchIntervalInBackground: false,
  });
  const health = query.data;
  const status = query.error ? 'Health check unavailable' : !health ? 'Checking…'
    : health.status === 'ready' ? 'Up to date' : health.status === 'syncing' ? 'Syncing in background' : 'Unavailable';
  const connection = startup.unreachable || backend.unreachable ? 'Reconnecting'
    : startup.status?.state === 'failed' ? 'Startup failed' : startup.status?.state === 'starting' ? 'Starting' : 'Connected';
  return <>
    <SettingRow label="Backend connection" desc="The app stays open during restarts and reconnects automatically."
      detail={startup.status?.state === 'failed' || startup.status?.state === 'starting' ? startup.status.phase : backend.error ?? undefined}>
      <span role="status">{connection}</span>
    </SettingRow>
    <SettingRow label="Conversation history" desc="History sync retries automatically. Analytics uses already-imported data while sync is incomplete."
      detail={health?.lastCheckedAt ? `Last successful check: ${new Date(health.lastCheckedAt).toLocaleString()}` : undefined}>
      <span role="status">{status}</span>
    </SettingRow>
    <SettingRow label="Background work" desc="Inspect history import progress and individual failures without interrupting active sessions.">
      <RouteButton to="/background-tasks" size="small" variant="ghost">Background tasks</RouteButton>
    </SettingRow>
  </>;
}
