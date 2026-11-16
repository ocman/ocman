import { useId, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Button, SelectField } from '../components/Control';
import { DataTable } from '../components/DataTable';
import { EmptyState } from '../components/EmptyState';
import { InlineAlert } from '../components/InlineAlert';
import { PageState } from '../components/PageState';
import { backgroundTasks } from '../lib/backgroundTasks';
import { groupBackgroundTasks, taskStatusLabels } from '../lib/backgroundTaskGroups';
import { useVisiblePollingQuery } from '../lib/useVisiblePollingQuery';
import { BackgroundTaskDetails, BackgroundTaskRow } from './BackgroundTaskRow';
import './BackgroundTasks.css';

export function BackgroundTasks() {
  const detailsId = useId();
  const overview = useRef<HTMLDivElement>(null);
  const [params, setParams] = useSearchParams();
  const rawStatus = params.get('taskStatus') ?? '';
  const status = rawStatus !== 'waiting' && Object.hasOwn(taskStatusLabels, rawStatus) ? rawStatus : '';
  const update = (name: string, value: string, reset = false) => setParams(previous => {
    const next = new URLSearchParams(previous);
    if (reset) { next.delete('taskGroup'); next.delete('taskPage'); }
    if (value) next.set(name, value);
    else next.delete(name);
    return next;
  });
  const closeDetails = () => {
    update('taskGroup', '');
    overview.current?.querySelector<HTMLButtonElement>('button[aria-expanded="true"]')?.focus();
  };
  const query = useVisiblePollingQuery({
    queryKey: ['background-tasks'], intervalMs: 2_000, timeoutMs: 10_000,
    run: (signal) => backgroundTasks('', signal),
  });
  const tasks = query.data ?? [];
  const groups = groupBackgroundTasks(tasks).filter(group => !status || group.current.status === status);
  const pages = Math.max(1, Math.ceil(groups.length / 5));
  const requestedPage = Number(params.get('taskPage') ?? 1);
  const page = Number.isSafeInteger(requestedPage) && requestedPage > 0 ? Math.min(requestedPage, pages) : 1;
  const selected = params.get('taskGroup');
  const focused = groups.find(group => group.key === selected);
  return <section className="background-tasks" aria-label="Ocman background tasks">
    <p>One row per task group. Latest status updates in place; select a group for progress and recent runs.</p>
    <div className="background-tasks-controls">
      <SelectField aria-label="Task status" value={status} onChange={(event) => update('taskStatus', event.target.value, true)}>
        <option value="">All statuses</option>
        {Object.entries(taskStatusLabels).filter(([value]) => value !== 'waiting').map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </SelectField>
      {status && <Button variant="ghost" onClick={() => update('taskStatus', '', true)}>Clear filters</Button>}
      <Button variant="ghost" disabled={query.isFetching} onClick={() => void query.refetch()}>Refresh</Button>
    </div>
    {query.isPending && !query.error && <div role="status" aria-label="Loading background tasks" className="background-tasks-skeleton">
      {Array.from({ length: 5 }, (_, index) => <div key={index} />)}
    </div>}
    {query.error && (query.data === undefined
      ? <PageState compact error title="Background tasks unavailable" description={query.error.message} onRetry={() => void query.refetch()} retrying={query.isFetching} />
      : <InlineAlert onRetry={() => void query.refetch()} retrying={query.isFetching}>Could not refresh background tasks: {query.error.message}</InlineAlert>)}
    {!query.isPending && !query.error && groups.length === 0 && <EmptyState>No background tasks match this view.</EmptyState>}
    <div className="background-tasks-workspace" data-details={!!selected}>
      {groups.length > 0 && <div className="background-tasks-overview" ref={overview}>
        <DataTable framed aria-label="Background task groups">
          <thead><tr><th>Task group</th><th>Latest status</th><th>Last run</th></tr></thead>
          <tbody>{groups.slice((page - 1) * 5, page * 5).map(({ key, current }) => <BackgroundTaskRow key={key}
            task={current} selected={key === selected} detailsId={detailsId}
            onSelect={() => update('taskGroup', key === selected ? '' : key)} />)}</tbody>
        </DataTable>
        <div className="background-tasks-pagination"><span>{groups.length} task {groups.length === 1 ? 'group' : 'groups'} · fixed order</span>
          {pages > 1 && <><Button variant="ghost" size="compact" aria-label="Previous task groups" disabled={page === 1}
            onClick={() => update('taskPage', String(page - 1), true)}>Previous</Button>
            <span>Page {page} of {pages}</span><Button variant="ghost" size="compact" aria-label="Next task groups" disabled={page === pages}
              onClick={() => update('taskPage', String(page + 1), true)}>Next</Button></>}
        </div>
      </div>}
      {focused && <BackgroundTaskDetails key={focused.key} id={detailsId} current={focused.current} runs={focused.runs} onClose={closeDetails} />}
      {selected && !focused && query.data !== undefined && <div className="background-task-details">
        <PageState compact title="Task group no longer available" description="It may be outside the available records or current filter." />
        <Button variant="ghost" onClick={closeDetails}>Close task details</Button>
      </div>}
    </div>
    {tasks.length >= 200 && <p>Tasks and history are limited to 200 available records. Older tasks and runs may not appear.</p>}
  </section>;
}
