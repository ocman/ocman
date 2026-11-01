import type { ComponentProps } from 'react';
import { prWorkflowApi } from '../../lib/prWorkflowApi';
import { useVisiblePollingQuery } from '../../lib/useVisiblePollingQuery';
import { BackgroundTasksNotice } from './BackgroundTasksNotice';

type Props = ComponentProps<typeof BackgroundTasksNotice> & { sessionId: string; platform: string };

export function SessionBackgroundTasksNotice({ sessionId, platform, ...props }: Props) {
  const query = useVisiblePollingQuery({
    queryKey: ['session-pr-background-tasks', platform, sessionId],
    run: () => prWorkflowApi.links(sessionId, platform),
    intervalMs: 5_000, timeoutMs: 10_000,
  });
  const watches = (query.data?.pullRequests ?? []).filter(link => link.watching);
  return <BackgroundTasksNotice {...props} sessionId={sessionId} platform={platform}
    additionalTasks={watches.map(link => ({
      id: `pr-watch:${link.remote.host}/${link.remote.repo}#${link.number}`,
      description: `Watching ${link.remote.repo}#${link.number}: ${link.pr.title}`,
      onStop: async () => {
        await prWorkflowApi.change(sessionId, platform, {
          action: 'unwatch', host: link.remote.host, repo: link.remote.repo, number: link.number,
        });
        await query.refetch();
      },
    }))} />;
}
