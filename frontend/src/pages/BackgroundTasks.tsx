import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Button, SelectField } from '../components/Control';
import { DataTableGroup, DataTableRow } from '../components/DataTable';
import { EmptyState } from '../components/EmptyState';
import { LoadingState } from '../components/LoadingState';
import { backgroundTasks, type BackgroundTask, type BackgroundTaskStatus } from '../lib/backgroundTasks';
import { withDeadline } from '../lib/coalescedRefresh';
import { useDocumentVisible } from '../lib/usePanelVisible';
import './BackgroundTasks.css';

const labels: Record<BackgroundTaskStatus, string> = {
  queued: 'Queued', running: 'Running', succeeded: 'Succeeded', failed: 'Failed',
  cancelled: 'Cancelled', interrupted: 'Interrupted',
};

function TaskProgress({ task }: { task: BackgroundTask }) {
  const { completed, total, unit, phase, counters } = task.progress;
  const known = total !== null && total > 0;
  return <div className="background-task-progress">
    <span>{phase}{unit && <> · {total === null ? `${completed} ${unit}` : `${completed} / ${total} ${unit}`}</>}</span>
    {(task.status === 'running' || task.status === 'queued' || known) && <progress
      aria-label={`Progress for ${task.title}`}
      value={known ? completed : undefined}
      max={known ? total : 1}
    />}
    {Object.keys(counters).length > 0 && <span className="background-task-counters">
      {Object.entries(counters).map(([name, value]) => <span key={name}>{name.replaceAll('-', ' ')}: {value}</span>)}
    </span>}
    {task.error && <p className="background-task-error" role="alert">{task.error}</p>}
  </div>;
}

function TaskGroup({ title, tasks }: { title: string; tasks: BackgroundTask[] }) {
  if (tasks.length === 0) return null;
  return <DataTableGroup label={title} noun="tasks" count={tasks.length}>
    {tasks.map((task) => <DataTableRow key={task.id}
      primary={<><strong>{task.title}</strong> <span className="background-task-state" data-status={task.status}>{labels[task.status]}</span></>}
      secondary={<TaskProgress task={task} />}
      meta={<time dateTime={new Date(task.updatedAt).toISOString()}>{new Date(task.updatedAt).toLocaleString()}</time>}
    />)}
  </DataTableGroup>;
}

export function BackgroundTasks() {
  const [status, setStatus] = useState<BackgroundTaskStatus | ''>('');
  const visible = useDocumentVisible();
  const query = useQuery({
    queryKey: ['background-tasks', status],
    queryFn: ({ signal }) => withDeadline(10_000, (deadline) => backgroundTasks(status, deadline), signal),
    enabled: visible,
    refetchInterval: 2_000,
    refetchIntervalInBackground: false,
  });
  const tasks = query.data ?? [];
  const active = tasks.filter((task) => task.status === 'queued' || task.status === 'running');
  const recent = tasks.filter((task) => task.status !== 'queued' && task.status !== 'running');
  return <section className="background-tasks" aria-label="Ocman background tasks">
    <p>Ocman work on this instance, separate from coding-agent tasks. History scans report sessions checked; failed sessions still need repair.</p>
    <div className="background-tasks-controls">
      <SelectField aria-label="Task status" value={status} onChange={(event) => setStatus(event.target.value as BackgroundTaskStatus | '')}>
        <option value="">All statuses</option>
        {Object.entries(labels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </SelectField>
      <Button variant="ghost" disabled={query.isFetching} onClick={() => void query.refetch()}>Refresh</Button>
    </div>
    {query.isPending && !query.error && <LoadingState>Loading background tasks…</LoadingState>}
    {query.error && <p role="alert">Could not load background tasks: {query.error.message}</p>}
    {!query.isPending && !query.error && tasks.length === 0 && <EmptyState>No background tasks match this view.</EmptyState>}
    <TaskGroup title="Active" tasks={active} />
    <TaskGroup title="Recent" tasks={recent} />
    {tasks.length >= 200 && <p>Showing up to 200 matching tasks, active work first.</p>}
  </section>;
}
