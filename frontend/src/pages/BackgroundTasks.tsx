import { useId, useState } from 'react';
import { Button, SelectField } from '../components/Control';
import { Spinner } from '../components/Spinner';
import { EmptyState } from '../components/EmptyState';
import { LoadingState } from '../components/LoadingState';
import { HostBadge } from '../components/HostBadge';
import { backgroundTasks, type BackgroundTask, type BackgroundTaskStatus } from '../lib/backgroundTasks';
import { useVisiblePollingQuery } from '../lib/useVisiblePollingQuery';
import { formatDuration, formatExactDate } from '../lib/format';
import './BackgroundTasks.css';

const labels: Record<BackgroundTaskStatus, string> = {
  queued: 'Queued', running: 'Running', succeeded: 'Succeeded', failed: 'Failed',
  cancelled: 'Cancelled', interrupted: 'Interrupted',
};

function TaskSchedule({ task }: { task: BackgroundTask }) {
  if (!task.schedule) return null;
  const { intervalMillis, nextRunAt, runs } = task.schedule;
  const timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return <div className="background-task-counters" aria-label={`Schedule for ${task.title}`}>
    <span>Recurring · every {formatDuration(intervalMillis)} · {runs} {runs === 1 ? 'run' : 'runs'}</span>
    {nextRunAt !== undefined && nextRunAt > 0 && <span>Next check: <time dateTime={new Date(nextRunAt).toISOString()}>{formatExactDate(nextRunAt)} {timeZone}</time></span>}
    {task.scope && task.scope !== 'local' && <span>Scope: {task.scope}</span>}
  </div>;
}

function TaskProgressBar({ task }: { task: BackgroundTask }) {
  const { completed, total } = task.progress;
  return total !== null && total > 0 ? <progress
    aria-label={`Progress for ${task.title}`} value={completed} max={total}
  /> : null;
}

function TaskProgress({ task }: { task: BackgroundTask }) {
  const { completed, total, unit, phase, counters } = task.progress;
  return <div className="background-task-progress">
    <TaskSchedule task={task} />
    <span>{phase}{unit && <> · {total === null ? `${completed} ${unit}` : `${completed} / ${total} ${unit}`}</>}</span>
    <TaskProgressBar task={task} />
    {Object.keys(counters).length > 0 && <span className="background-task-counters">
      {Object.entries(counters).map(([name, value]) => <span key={name}>{name.replaceAll('-', ' ')}: {value}</span>)}
    </span>}
    {task.error && <p className="background-task-error" role="alert">{task.error}</p>}
  </div>;
}

function groupTasks(tasks: BackgroundTask[]) {
  const groups = new Map<string, BackgroundTask[]>();
  for (const task of tasks) {
    const key = JSON.stringify([task.ownerId, task.kind, task.key || task.title, task.scope ?? '']);
    const group = groups.get(key);
    if (group) group.push(task);
    else groups.set(key, [task]);
  }
  return Array.from(groups, ([key, runs]) => ({
    key, runs: runs.sort((a, b) => b.createdAt - a.createdAt),
  }));
}

function TaskRow({ runs }: { runs: BackgroundTask[] }) {
  const task = runs[0];
  const id = useId();
  const [open, setOpen] = useState(false);
  return <div role="listitem">
    <Button variant="ghost" size="compact" className="background-task-row"
      aria-expanded={open} aria-controls={open ? id : undefined}
      onClick={() => setOpen(!open)}>
      <strong>{task.title}</strong>
      <span className="background-task-state" data-status={task.status}>
        {task.status === 'running' && <Spinner />}{labels[task.status]}
      </span>
      <HostBadge alwaysShow remoteId={task.ownerId} remoteName={task.ownerId === 'local' ? 'This machine' : task.ownerId} />
      {task.scope && task.scope !== 'local' && <span className="background-task-scope">{task.scope}</span>}
      <span className="background-task-count">{runs.length} {runs.length === 1 ? 'run' : 'runs'}</span>
      <span className="background-task-phase">{task.progress.phase}</span>
      <TaskProgressBar task={task} />
      <i className={`bi bi-chevron-${open ? 'down' : 'right'}`} aria-hidden="true" />
    </Button>
    {open && <section id={id} aria-label={`History for ${task.title}`} className="background-task-history">
      <ol aria-label={`Runs for ${task.title}`}>
        {runs.map((run) => <li key={run.id}>
          <div className="background-task-counters">
            <span className="background-task-state" data-status={run.status}>{labels[run.status]}</span>
            <span>Created <time dateTime={new Date(run.createdAt).toISOString()}>{formatExactDate(run.createdAt)}</time></span>
            <span>Updated <time dateTime={new Date(run.updatedAt).toISOString()}>{formatExactDate(run.updatedAt)}</time></span>
          </div>
          <TaskProgress task={run} />
        </li>)}
      </ol>
    </section>}
  </div>;
}

export function BackgroundTasks() {
  const [status, setStatus] = useState<BackgroundTaskStatus | ''>('');
  const query = useVisiblePollingQuery({
    queryKey: ['background-tasks', status], intervalMs: 2_000, timeoutMs: 10_000,
    run: (signal) => backgroundTasks(status, signal),
  });
  const tasks = query.data ?? [];
  return <section className="background-tasks" aria-label="Ocman background tasks">
    <p>Background work on this machine and connected remotes. Expand a task for its run history, progress and schedule details.</p>
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
    {tasks.length > 0 && <div role="list" aria-label="Background tasks">
      {groupTasks(tasks).map(({ key, runs }) => <TaskRow key={key} runs={runs} />)}
    </div>}
    {tasks.length >= 200 && <p>Showing up to 200 matching tasks, active work first.</p>}
  </section>;
}
