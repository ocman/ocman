import { useSearchParams } from 'react-router-dom';
import { Button, SelectField } from '../components/Control';
import { EmptyState } from '../components/EmptyState';
import { LoadingState } from '../components/LoadingState';
import { backgroundTasks } from '../lib/backgroundTasks';
import { groupBackgroundTasks, taskDisplayStatus, taskStatusLabels } from '../lib/backgroundTaskGroups';
import { useVisiblePollingQuery } from '../lib/useVisiblePollingQuery';
import { BackgroundTaskRow } from './BackgroundTaskRow';
import './BackgroundTasks.css';

export function BackgroundTasks() {
  const [params, setParams] = useSearchParams();
  const rawStatus = params.get('taskStatus') ?? '';
  const status = Object.hasOwn(taskStatusLabels, rawStatus) ? rawStatus : '';
  const setStatus = (value: string) => setParams(previous => {
    const next = new URLSearchParams(previous);
    if (value) next.set('taskStatus', value);
    else next.delete('taskStatus');
    return next;
  });
  const query = useVisiblePollingQuery({
    queryKey: ['background-tasks'], intervalMs: 2_000, timeoutMs: 10_000,
    run: (signal) => backgroundTasks('', signal),
  });
  const tasks = query.data ?? [];
  // Filter current task state, not past runs that could misrepresent today's work.
  const groups = groupBackgroundTasks(tasks).filter(group => !status || taskDisplayStatus(group.current) === status);
  return <section className="background-tasks" aria-label="Ocman background tasks">
    <p>One row per task on this machine and connected remotes. Progress updates in place; expand a task for available run history.</p>
    <div className="background-tasks-controls">
      <SelectField aria-label="Task status" value={status} onChange={(event) => setStatus(event.target.value)}>
        <option value="">All statuses</option>
        {Object.entries(taskStatusLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </SelectField>
      {status && <Button variant="ghost" onClick={() => setStatus('')}>Clear filters</Button>}
      <Button variant="ghost" disabled={query.isFetching} onClick={() => void query.refetch()}>Refresh</Button>
    </div>
    {query.isPending && !query.error && <LoadingState>Loading background tasks…</LoadingState>}
    {query.error && <p role="alert">Could not load background tasks: {query.error.message}</p>}
    {!query.isPending && !query.error && groups.length === 0 && <EmptyState>No background tasks match this view.</EmptyState>}
    {groups.length > 0 && <div role="list" aria-label="Background tasks">
      {groups.map(({ key, runs, current }) => <BackgroundTaskRow key={key} runs={runs} current={current} />)}
    </div>}
    {tasks.length >= 200 && <p>Tasks and history are limited to 200 available records. Older tasks and runs may not appear.</p>}
  </section>;
}
