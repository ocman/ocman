import { useId, useRef, useState, type CSSProperties } from 'react';
import { Button, SelectField } from '../components/Control';
import { Spinner } from '../components/Spinner';
import { Popover } from '../components/Popover';
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

type TaskSelection = { selected: string | null; onSelect: (id: string | null) => void };

function TaskRow({ task, selected, onSelect }: { task: BackgroundTask } & TaskSelection) {
  const trigger = useRef<HTMLButtonElement>(null);
  const id = useId();
  const [position, setPosition] = useState<CSSProperties>();
  const open = selected === id && !!position;
  return <div role="listitem">
    <Button ref={trigger} variant="ghost" size="compact" className="background-task-row"
      aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? id : undefined}
      onClick={() => {
        const rect = trigger.current!.getBoundingClientRect();
        onSelect(open ? null : id);
        setPosition({
          left: Math.max(12, Math.min(rect.left, window.innerWidth - 364)),
          ...(rect.bottom < window.innerHeight / 2 ? { top: rect.bottom + 4 } : { bottom: window.innerHeight - rect.top + 4 }),
          maxHeight: Math.max(100, window.innerHeight / 2 - 24),
        });
      }}>
      <strong>{task.title}</strong>
      <span className="background-task-state" data-status={task.status}>
        {task.status === 'running' && <Spinner />}{labels[task.status]}
      </span>
      <HostBadge alwaysShow remoteId={task.ownerId} remoteName={task.ownerId === 'local' ? 'This machine' : task.ownerId} />
      <span className="background-task-phase">{task.progress.phase}</span>
      <TaskProgressBar task={task} />
      <i className="bi bi-info-circle" aria-hidden="true" />
    </Button>
    <Popover open={open} onClose={() => onSelect(null)} triggerRef={trigger}
      id={id} label={`Details for ${task.title}`} className="background-task-details" style={position}>
      <strong>{task.title}</strong>
      <TaskProgress task={task} />
      <p>Updated <time dateTime={new Date(task.updatedAt).toISOString()}>{formatExactDate(task.updatedAt)}</time></p>
    </Popover>
  </div>;
}

export function BackgroundTasks() {
  const [status, setStatus] = useState<BackgroundTaskStatus | ''>('');
  const [selected, setSelected] = useState<string | null>(null);
  const query = useVisiblePollingQuery({
    queryKey: ['background-tasks', status], intervalMs: 2_000, timeoutMs: 10_000,
    run: (signal) => backgroundTasks(status, signal),
  });
  const tasks = query.data ?? [];
  return <section className="background-tasks" aria-label="Ocman background tasks">
    <p>Background work on this machine and connected remotes. Select a task for progress and schedule details.</p>
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
      {tasks.map((task) => <TaskRow key={`${task.ownerId}:${task.id}`} task={task} selected={selected} onSelect={setSelected} />)}
    </div>}
    {tasks.length >= 200 && <p>Showing up to 200 matching tasks, active work first.</p>}
  </section>;
}
