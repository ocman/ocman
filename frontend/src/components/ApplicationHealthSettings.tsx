import { useQuery } from '@tanstack/react-query';
import { fetchJSON } from '../lib/api.requests';
import { withDeadline } from '../lib/coalescedRefresh';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { useBackendStatus } from '../lib/backendStatus';
import { RouteButton } from './Control';
import { SettingRow } from './SettingRow';

interface HistoryHealth {
  status: 'ready' | 'syncing' | 'unavailable';
  sources: number;
  lastCheckedAt?: string;
}

interface BackendHealth {
  state: 'starting' | 'ready' | 'failed';
  phase: string;
}

async function readBackendHealth(signal: AbortSignal): Promise<BackendHealth> {
  const response = await fetch('/api/startup', { cache: 'no-store', signal });
  if (response.status === 404) return { state: 'ready', phase: 'Ready' };
  const health: BackendHealth = await response.json();
  if (!response.ok || !['starting', 'ready', 'failed'].includes(health.state) || typeof health.phase !== 'string') {
    throw new Error('Backend health unavailable');
  }
  return health;
}

export function ApplicationHealthSettings() {
  const visible = useDocumentVisible();
  const backend = useBackendStatus();
  const startup = useQuery({
    queryKey: ['application-health', 'backend'],
    queryFn: ({ signal }) => withDeadline(3_000, readBackendHealth, signal),
    enabled: visible,
    refetchInterval: 5_000,
    refetchIntervalInBackground: false,
  });
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
  const connection = startup.isError || backend.unreachable ? 'Reconnecting'
    : startup.data?.state === 'failed' ? 'Startup failed' : startup.data?.state === 'starting' ? 'Starting'
      : startup.isPending ? 'Checking…' : 'Connected';
  return <>
    <SettingRow label="Backend connection" desc="The app stays open during restarts and reconnects automatically."
      detail={startup.data?.state === 'failed' || startup.data?.state === 'starting' ? startup.data.phase : backend.error ?? undefined}>
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
