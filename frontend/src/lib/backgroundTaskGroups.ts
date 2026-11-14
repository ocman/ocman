import type { BackgroundTask, BackgroundTaskStatus } from './backgroundTasks';

export const taskStatusLabels: Record<BackgroundTaskStatus | 'waiting', string> = {
  queued: 'Queued', running: 'Running', succeeded: 'Succeeded', failed: 'Failed',
  cancelled: 'Cancelled', interrupted: 'Interrupted', waiting: 'Waiting for next run',
};

export function taskDisplayStatus(task: BackgroundTask): BackgroundTaskStatus | 'waiting' {
  return task.status === 'succeeded' && (task.schedule?.nextRunAt ?? 0) > 0 ? 'waiting' : task.status;
}

function newestFirst(a: BackgroundTask, b: BackgroundTask) {
  return b.createdAt - a.createdAt || b.updatedAt - a.updatedAt || a.id.localeCompare(b.id);
}

export function groupBackgroundTasks(tasks: BackgroundTask[]) {
  const groups = new Map<string, BackgroundTask[]>();
  for (const task of tasks) {
    const key = JSON.stringify([task.ownerId, task.kind, task.key || task.title, task.scope ?? '']);
    const group = groups.get(key);
    if (group) group.push(task);
    else groups.set(key, [task]);
  }
  // Identity order is independent of activity timestamps and execution status.
  return Array.from(groups, ([key, runs]) => {
    runs.sort(newestFirst);
    const current = runs.find(run => run.status === 'running') ?? runs.find(run => run.status === 'queued') ?? runs[0];
    return { key, runs, current };
  }).sort((a, b) => a.key.localeCompare(b.key));
}
