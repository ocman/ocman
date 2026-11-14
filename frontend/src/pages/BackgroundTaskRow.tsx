import { useId, useState } from 'react';
import { Button } from '../components/Control';
import { Spinner } from '../components/Spinner';
import { HostBadge } from '../components/HostBadge';
import type { BackgroundTask } from '../lib/backgroundTasks';
import { taskDisplayStatus, taskStatusLabels } from '../lib/backgroundTaskGroups';
import { formatDuration, formatExactDate } from '../lib/format';

function TaskSchedule({ task }: { task: BackgroundTask }) {
  if (!task.schedule) return null;
  const { intervalMillis, nextRunAt } = task.schedule;
  const timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return <div className="background-task-counters" aria-label={`Schedule for ${task.title}`}>
    <span>Recurring · every {formatDuration(intervalMillis)}</span>
    {nextRunAt !== undefined && nextRunAt > 0 && <span>Next check: <time dateTime={new Date(nextRunAt).toISOString()}>{formatExactDate(nextRunAt)} {timeZone}</time></span>}
  </div>;
}

function TaskProgress({ task }: { task: BackgroundTask }) {
  const { completed, total, unit, phase, counters } = task.progress;
  const active = task.status === 'running';
  const known = total !== null && total > 0;
  return <div className="background-task-progress">
    {phase && <span>{phase}</span>}
    {known ? <>
      <span>{completed} / {total}{unit ? ` ${unit}` : ''}</span>
      <progress aria-label={`Progress for ${task.title}`} value={completed} max={total} />
    </> : <>
      {(active || completed > 0) && <span>{completed}{unit ? ` ${unit}` : ' processed'}{active ? ' · total unknown' : ''}</span>}
      {active && <progress aria-label={`Progress for ${task.title}`} />}
    </>}
    {Object.keys(counters).length > 0 && <span className="background-task-counters">
      {Object.entries(counters).map(([name, value]) => <span key={name}>{name.replaceAll('-', ' ')}: {value}</span>)}
    </span>}
    {task.error && <p className="background-task-error" role="alert">{task.error}</p>}
  </div>;
}

export function BackgroundTaskRow({ runs, current: task }: { runs: BackgroundTask[]; current: BackgroundTask }) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const status = taskDisplayStatus(task);
  const count = task.schedule?.runs ?? runs.length;
  return <div role="listitem" className="background-task" aria-label={task.title}>
    <Button variant="ghost" size="compact" className="background-task-row"
      aria-expanded={open} aria-controls={open ? id : undefined}
      onClick={() => setOpen(!open)}>
      <strong>{task.title}</strong>
      <span className="background-task-state" data-status={status}>
        {status === 'running' && <Spinner />}{taskStatusLabels[status]}
      </span>
      <span className="background-task-count">{count} {count === 1 ? 'run' : 'runs'}</span>
      <i className={`bi bi-chevron-${open ? 'down' : 'right'}`} aria-hidden="true" />
    </Button>
    <div className="background-task-summary" aria-label={`Current progress for ${task.title}`}>
      <div className="background-task-counters">
        <HostBadge alwaysShow remoteId={task.ownerId} remoteName={task.ownerId === 'local' ? 'This machine' : task.ownerId} />
        {task.scope && task.scope !== 'local' && <span className="background-task-scope">{task.scope}</span>}
        {status === 'waiting' && <span>Last run: {taskStatusLabels[task.status]}</span>}
      </div>
      <TaskProgress task={task} />
      <TaskSchedule task={task} />
    </div>
    {open && <section id={id} aria-label={`History for ${task.title}`} className="background-task-history">
      <p>Available run history{task.schedule ? '. Recurring jobs retain only their latest run.' : '.'}</p>
      <ol aria-label={`Runs for ${task.title}`}>
        {runs.map((run) => <li key={run.id}>
          <div className="background-task-counters">
            <span className="background-task-state" data-status={run.status}>{taskStatusLabels[run.status]}</span>
            <span>Created <time dateTime={new Date(run.createdAt).toISOString()}>{formatExactDate(run.createdAt)}</time></span>
            <span>Updated <time dateTime={new Date(run.updatedAt).toISOString()}>{formatExactDate(run.updatedAt)}</time></span>
          </div>
          <TaskProgress task={run} />
        </li>)}
      </ol>
    </section>}
  </div>;
}
