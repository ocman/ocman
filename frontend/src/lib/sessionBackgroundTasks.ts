import type { SessionView } from './sessionViewTypes';

export interface BackgroundTask { id: string; description: string }

export function reduceBackgroundTaskCount(state: SessionView, count: unknown, rawTasks?: unknown): SessionView {
  if (typeof count !== 'number' || !Number.isSafeInteger(count) || count < 0) return state;
  const tasks: BackgroundTask[] = [];
  const seen = new Set<string>();
  if (count > 0 && Array.isArray(rawTasks)) {
    for (const task of rawTasks) {
      if (!task || typeof task.id !== 'string' || !task.id || typeof task.description !== 'string' || seen.has(task.id)) continue;
      tasks.push({ id: task.id, description: task.description });
      seen.add(task.id);
    }
  }
  if (count === state.backgroundTaskCount && JSON.stringify(tasks) === JSON.stringify(state.backgroundTasks ?? [])) return state;
  return { ...state, backgroundTaskCount: count, backgroundTasks: tasks };
}

export function backgroundSnapshotCount(snapshot: unknown, sessionId: string): unknown {
  if (!snapshot || typeof snapshot !== 'object') return 0;
  return (snapshot as Record<string, unknown>)[sessionId] ?? 0;
}
