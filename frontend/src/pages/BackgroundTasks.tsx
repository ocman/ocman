import { useId, useRef, useState, type CSSProperties } from 'react';
import { Button, SelectField } from '../components/Control';
import { DataTableGroup } from '../components/DataTable';
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

function TaskProgress({ task }: { task: BackgroundTask }) {
  const { completed, total, unit, phase, counters } = task.progress;
  const known = total !== null && total > 0;
  return <div className="background-task-progress">
    <TaskSchedule task={task} />
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

type TaskSelection = { selected: string | null; onSelect: (id: string | null) => void };

function TaskGroup({ title, tasks, ...selection }: { title: string; tasks: BackgroundTask[] } & TaskSelection) {
  if (tasks.length === 0) return null;
  return <DataTableGroup label={title} noun="tasks" count={tasks.length}>
    {tasks.map((task) => <TaskRow key={`${task.ownerId}:${task.id}`} task={task} {...selection} />)}
  </DataTableGroup>;
}

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
      <span className="background-task-state" data-status={task.status}>{labels[task.status]}</span>
      <HostBadge alwaysShow remoteId={task.ownerId} remoteName={task.ownerId === 'local' ? 'This machine' : task.ownerId} />
      <span className="background-task-phase">{task.progress.phase}</span>
      <i className="bi bi-info-circle" aria-hidden="true" />
    </Button>
    <Popover open={open} onClose={() => onSelect(null)} triggerRef={trigger}
      id={id} label={`Details for ${task.title}`} style={position}>
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
  const active = tasks.filter((task) => task.status === 'queued' || task.status === 'running');
  const recent = tasks.filter((task) => task.status !== 'queued' && task.status !== 'running');
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
    <TaskGroup title="Active" tasks={active} selected={selected} onSelect={setSelected} />
    <TaskGroup title="Recent" tasks={recent} selected={selected} onSelect={setSelected} />
    {tasks.length >= 200 && <p>Showing up to 200 matching tasks, active work first.</p>}
  </section>;
}
