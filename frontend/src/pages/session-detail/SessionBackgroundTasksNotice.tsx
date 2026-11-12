import type { ComponentProps } from 'react';
import { Button } from '../../components/Control';
import { prWorkflowApi } from '../../lib/prWorkflowApi';
import { useVisiblePollingQuery } from '../../lib/useVisiblePollingQuery';
import { useSessionLiveRefresh } from '../../lib/useSessionLiveRefresh';
import { BackgroundTasksNotice } from './BackgroundTasksNotice';

type Props = ComponentProps<typeof BackgroundTasksNotice> & { sessionId: string; platform: string };

export function SessionBackgroundTasksNotice({ sessionId, platform, ...props }: Props) {
  const query = useVisiblePollingQuery({
    queryKey: ['session-pr-background-tasks', platform, sessionId],
    run: signal => prWorkflowApi.background(sessionId, platform, signal),
    intervalMs: 5_000, timeoutMs: 10_000,
  });
  useSessionLiveRefresh(sessionId, platform, query.refetch);
  const watches = (query.data?.pullRequests ?? []).filter(link => link.backgroundTask ?? link.watching);
  return <><BackgroundTasksNotice {...props} sessionId={sessionId} platform={platform}
    additionalTasks={watches.map(link => ({
      id: `pr-watch:${link.remote.host}/${link.remote.repo}#${link.number}`,
      description: `${link.watching ? 'Watching' : 'PR update awaiting delivery for'} ${link.remote.repo}#${link.number}: ${link.pr.title}`,
      onStop: async () => {
        await prWorkflowApi.change(sessionId, platform, {
          action: 'unwatch', host: link.remote.host, repo: link.remote.repo, number: link.number,
        });
        await query.refetch();
      },
    }))} />
    {query.error && <span role="alert" title={query.error.message}>
      <i className="bi bi-exclamation-triangle" aria-hidden="true" />{' '}
      {query.data ? 'PR watch status may be outdated.' : 'PR watch status unavailable.'}{' '}
      <Button variant="ghost" size="compact" aria-label="Retry PR watch status" disabled={query.isFetching}
        onClick={() => void query.refetch()}>Retry</Button>
    </span>}
  </>;
}
