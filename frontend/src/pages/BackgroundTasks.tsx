import { useId, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Button, SelectField } from '../components/Control';
import { DataTable } from '../components/DataTable';
import { EmptyState } from '../components/EmptyState';
import { InlineAlert } from '../components/InlineAlert';
import { PageState } from '../components/PageState';
import { backgroundTasks } from '../lib/backgroundTasks';
import { groupBackgroundTasks, taskStatusLabels } from '../lib/backgroundTaskGroups';
import { categorizeBackgroundTasks } from '../lib/backgroundTaskCategories';
import { useVisiblePollingQuery } from '../lib/useVisiblePollingQuery';
import { BackgroundTaskDetails, BackgroundTaskRow } from './BackgroundTaskRow';
import { BackgroundTaskMachineSelect } from './BackgroundTaskMachineSelect';
import './BackgroundTasks.css';

export function BackgroundTasks() {
  const detailsId = useId();
  const overview = useRef<HTMLDivElement>(null);
  const [params, setParams] = useSearchParams();
  const rawStatus = params.get('taskStatus') ?? '';
  const machine = params.get('taskMachine') || 'local';
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
    queryKey: ['background-tasks', machine], intervalMs: 2_000, timeoutMs: 10_000,
    run: (signal) => backgroundTasks('', signal, machine),
  });
  const tasks = query.data ?? [];
  const groups = groupBackgroundTasks(tasks).filter(group => !status || group.current.status === status);
  const categories = categorizeBackgroundTasks(groups);
  const selected = params.get('taskGroup');
  const focused = groups.find(group => group.key === selected);
  return <section className="background-tasks" aria-label="Ocman background tasks">
    <p>Tasks grouped by purpose and sorted by name. Select a task for progress and recent runs.</p>
    <div className="background-tasks-controls" role="group" aria-label="Task controls">
      <SelectField aria-label="Task status" value={status} onChange={(event) => update('taskStatus', event.target.value, true)}>
        <option value="">All statuses</option>
        {Object.entries(taskStatusLabels).filter(([value]) => value !== 'waiting').map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </SelectField>
      {status && <Button variant="ghost" onClick={() => update('taskStatus', '', true)}>Clear filters</Button>}
      <Button variant="ghost" disabled={query.isFetching} onClick={() => void query.refetch()}>Refresh</Button>
      <BackgroundTaskMachineSelect value={machine} onChange={owner => update('taskMachine', owner === 'local' ? '' : owner, true)} />
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
        <div className="background-tasks-scroll" role="region" aria-label="Task list" tabIndex={0} key={machine}>
          <DataTable framed aria-label="Background task groups">
            <thead><tr><th>Task group</th><th>Latest status</th><th>Last run</th></tr></thead>
            {categories.map(category => <tbody key={category.label} aria-label={category.label}>
              <tr className="background-task-category"><th colSpan={3} scope="rowgroup">
                <h3>{category.label} <span>{category.groups.length}</span></h3>
              </th></tr>
              {category.groups.map(({ key, current }) => <BackgroundTaskRow key={key}
                task={current} selected={key === selected} detailsId={detailsId}
                onSelect={() => update('taskGroup', key === selected ? '' : key)} />)}
            </tbody>)}
          </DataTable>
        </div>
        <p className="background-tasks-count">{groups.length} task {groups.length === 1 ? 'group' : 'groups'}</p>
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
