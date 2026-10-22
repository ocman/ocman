import { useQuery } from '@tanstack/react-query';
import { backgroundTaskCount } from '../lib/backgroundTasks';
import { withDeadline } from '../lib/coalescedRefresh';
import { useDocumentVisible } from '../lib/usePanelVisible';
import { RouteButton } from './Control';

export function BackgroundTaskCounter({ enabled }: { enabled: boolean }) {
  const visible = useDocumentVisible();
  const query = useQuery({
    queryKey: ['background-task-count'],
    queryFn: ({ signal }) => withDeadline(10_000, backgroundTaskCount, signal),
    enabled: enabled && visible,
    refetchInterval: 5_000,
    refetchIntervalInBackground: false,
  });
  const count = query.data?.count;
  return <RouteButton to="/background-tasks" variant="ghost" size="compact" className="backend-stats-counter"
    title="Queued and running background tasks"
    aria-label={count === undefined ? 'Background tasks: count unavailable' : `Active background tasks: ${count}`}>
    <span className="backend-stats-label">bgt</span>: {count ?? '—'}
  </RouteButton>;
}
