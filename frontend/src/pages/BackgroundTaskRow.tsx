import { Button } from '../components/Control';
import { Spinner } from '../components/Spinner';
import { HostBadge } from '../components/HostBadge';
import { RelativeTimestamp } from '../components/RelativeTime';
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

function TaskState({ task }: { task: BackgroundTask }) {
  return <span className="background-task-state" data-status={task.status}>
    {task.status === 'running' && <Spinner />}{taskStatusLabels[task.status]}
  </span>;
}

export function BackgroundTaskRow({ task, selected, onSelect, detailsId }: {
  task: BackgroundTask; selected: boolean; onSelect: () => void; detailsId: string;
}) {
  return <tr data-selected={selected} onClick={event => {
    if (!(event.target as HTMLElement).closest('button, a, time') && !window.getSelection()?.toString()) onSelect();
  }}>
    <td><Button variant="link" size="compact" aria-expanded={selected}
      aria-controls={selected ? detailsId : undefined} onClick={onSelect}>{task.title}</Button>
      <div className="background-task-owner"><HostBadge alwaysShow remoteId={task.ownerId}
        remoteName={task.ownerId === 'local' ? 'This machine' : task.ownerId} />
        {task.scope && task.scope !== 'local' && <span>{task.scope}</span>}
      </div>
    </td>
    <td data-label="Latest status"><TaskState task={task} /></td>
    <td data-label="Last run"><RelativeTimestamp value={task.startedAt ?? task.createdAt} /></td>
  </tr>;
}

export function BackgroundTaskDetails({ runs, current: task, id, onClose }: {
  runs: BackgroundTask[]; current: BackgroundTask; id: string; onClose: () => void;
}) {
  const count = task.schedule?.runs ?? runs.length;
  return <section id={id} aria-label={`Task group details for ${task.title}`} className="background-task-details">
    <div className="background-task-detail-heading"><h3>{task.title}</h3>
      <Button variant="ghost" size="compact" onClick={onClose} aria-label="Close task details"><i className="bi bi-x-lg" aria-hidden="true" /></Button>
    </div>
    <div className="background-task-counters"><TaskState task={task} />
      <HostBadge alwaysShow remoteId={task.ownerId} remoteName={task.ownerId === 'local' ? 'This machine' : task.ownerId} />
      {task.scope && task.scope !== 'local' && <span>{task.scope}</span>}
      <span>{count} {count === 1 ? 'run' : 'runs'}</span>
    </div>
    <div aria-label={`Current progress for ${task.title}`}><TaskProgress task={task} /></div>
    {taskDisplayStatus(task) === 'waiting' && <p>Waiting for next run · Last run: {taskStatusLabels[task.status]}</p>}
    <TaskSchedule task={task} />
    <section aria-label={`History for ${task.title}`} className="background-task-history">
      <h4>Recent runs</h4>
      <p>Available run history{task.schedule ? '. Recurring jobs retain only their latest run.' : '.'}</p>
      {runs.length > 5 && <p>Showing the latest 5 of {runs.length} available runs.</p>}
      <ol aria-label={`Runs for ${task.title}`}>
        {runs.slice(0, 5).map(run => <li key={run.id}>
          <details><summary><TaskState task={run} /> · {formatExactDate(run.createdAt)}</summary>
            <p>Updated <time dateTime={new Date(run.updatedAt).toISOString()}>{formatExactDate(run.updatedAt)}</time></p>
            <TaskProgress task={run} />
          </details>
        </li>)}
      </ol>
    </section>
  </section>;
}
