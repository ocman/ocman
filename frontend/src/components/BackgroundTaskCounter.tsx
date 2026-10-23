import { backgroundTaskCount } from '../lib/backgroundTasks';
import { useVisiblePollingQuery } from '../lib/useVisiblePollingQuery';
import { RouteButton } from './Control';

export function BackgroundTaskCounter({ enabled }: { enabled: boolean }) {
  const query = useVisiblePollingQuery({
    queryKey: ['background-task-count'], run: backgroundTaskCount, intervalMs: 5_000, timeoutMs: 10_000, enabled,
  });
  const count = query.data?.count;
  return <RouteButton to="/background-tasks" variant="ghost" size="compact" className="backend-stats-counter"
    title="Queued and running background tasks"
    aria-label={count === undefined ? 'Background tasks: count unavailable' : `Active background tasks: ${count}`}>
    <span className="backend-stats-label">bgt</span>: {count ?? '—'}
  </RouteButton>;
}
