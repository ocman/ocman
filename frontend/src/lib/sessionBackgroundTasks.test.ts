import { describe, expect, it } from 'vitest';
import { initialSessionView, reduceSessionView } from './sessionReducer';
import { reduceBackgroundTaskCount } from './sessionBackgroundTasks';

describe('background task counts', () => {
  it('replaces live task details even when the count is unchanged, validates rows and clears old details', () => {
    const tasks = [{ id: 'one', description: 'pnpm test' }];
    let view = reduceBackgroundTaskCount(initialSessionView('s1'), 1, [...tasks, null, {}, tasks[0]]);
    expect(view.backgroundTasks).toEqual(tasks);
    for (const mode of ['replace', 'reconcile'] as const) {
      expect(reduceSessionView(view, { type: 'load', view: initialSessionView('s1'), mode }).backgroundTasks).toEqual(tasks);
    }
    expect(reduceSessionView(view, { type: 'load', view: initialSessionView('s2') }).backgroundTasks).toEqual([]);
    view = reduceBackgroundTaskCount(view, 1, [{ id: 'two', description: 'go test' }]);
    expect(view.backgroundTasks).toEqual([{ id: 'two', description: 'go test' }]);
    expect(reduceBackgroundTaskCount(view, 1).backgroundTasks).toEqual([]);
  });

  it('tracks absolute counts and ignores invalid counts', () => {
    let view = reduceBackgroundTaskCount(initialSessionView('s1'), 2);
    expect(view.backgroundTaskCount).toBe(2);
    for (const count of [-1, 1.5, '3', null, Infinity]) {
      expect(reduceBackgroundTaskCount(view, count)).toBe(view);
    }
    expect(reduceBackgroundTaskCount(view, 2)).toBe(view);
    view = reduceBackgroundTaskCount(view, 0);
    expect(view.backgroundTaskCount).toBe(0);
  });

  it('preserves live counts across REST loads, but not navigation', () => {
    const view = reduceBackgroundTaskCount(initialSessionView('s1'), 3);
    for (const mode of ['replace', 'reconcile'] as const) {
      expect(reduceSessionView(view, { type: 'load', view: initialSessionView('s1'), mode }).backgroundTaskCount).toBe(3);
    }
    expect(reduceSessionView(view, { type: 'load', view: initialSessionView('s2') }).backgroundTaskCount).toBe(0);
  });
});
