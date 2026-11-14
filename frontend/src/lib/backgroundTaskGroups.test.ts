import { describe, expect, it } from 'vitest';
import type { BackgroundTask } from './backgroundTasks';
import { groupBackgroundTasks, taskDisplayStatus } from './backgroundTaskGroups';

function task(id: string, overrides: Partial<BackgroundTask> = {}): BackgroundTask {
  return { id, key: 'scan', title: 'Scan', kind: 'history', ownerId: 'local', status: 'succeeded',
    createdAt: 1000, updatedAt: 2000, progress: { phase: 'Checking', completed: 0, total: null, counters: {} }, ...overrides };
}

describe('background task grouping', () => {
  it('keeps owners, kinds, keys and scopes separate and falls back to title for unkeyed work', () => {
    const tasks = [task('one'), task('two'), task('remote', { ownerId: 'remote' }), task('kind', { kind: 'import' }),
      task('key', { key: 'other' }), task('scope', { scope: 'project' }), task('title', { key: undefined })];
    const groups = groupBackgroundTasks(tasks);
    expect(groups).toHaveLength(6);
    expect(groups.find(group => group.runs.length === 2)?.runs.map(run => run.id)).toEqual(['one', 'two']);
    expect(groupBackgroundTasks([...tasks].reverse()).map(group => group.key)).toEqual(groups.map(group => group.key));
    expect(tasks.map(run => run.id)).toEqual(['one', 'two', 'remote', 'kind', 'key', 'scope', 'title']);
  });

  it('represents running work before queued or newer finished runs without changing history order', () => {
    const running = task('running', { status: 'running' });
    const queued = task('queued', { status: 'queued', createdAt: 2000 });
    const finished = task('finished', { createdAt: 3000 });
    const group = groupBackgroundTasks([finished, queued, running])[0];
    expect(group.current).toBe(running);
    expect(group.runs.map(run => run.id)).toEqual(['finished', 'queued', 'running']);
    expect(groupBackgroundTasks([finished, queued])[0].current).toBe(queued);
    expect(groupBackgroundTasks([finished, task('older')])[0].current).toBe(finished);
  });

  it('breaks equal creation timestamps by update time then ID', () => {
    expect(groupBackgroundTasks([task('b'), task('a'), task('updated', { updatedAt: 3000 })])[0].runs.map(run => run.id))
      .toEqual(['updated', 'a', 'b']);
    expect(groupBackgroundTasks([])).toEqual([]);
  });

  it('only marks a successful scheduled task with a next check as waiting', () => {
    const scheduled = { intervalMillis: 60_000, runs: 3, nextRunAt: 4000 };
    expect(taskDisplayStatus(task('waiting', { schedule: scheduled }))).toBe('waiting');
    for (const status of ['queued', 'running', 'failed', 'cancelled', 'interrupted'] as const) {
      expect(taskDisplayStatus(task(status, { status, schedule: scheduled }))).toBe(status);
    }
    expect(taskDisplayStatus(task('stopped', { schedule: { ...scheduled, nextRunAt: 0 } }))).toBe('succeeded');
    expect(taskDisplayStatus(task('no-hint', { schedule: { intervalMillis: 60_000, runs: 1 } }))).toBe('succeeded');
    expect(taskDisplayStatus(task('once'))).toBe('succeeded');
  });
});
