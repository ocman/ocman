import type { SessionView } from './sessionViewTypes';

export interface BackgroundTask { id: string; description: string; kind?: 'agent' }

export function reduceBackgroundTaskCount(state: SessionView, count: unknown, rawTasks?: unknown): SessionView {
  if (typeof count !== 'number' || !Number.isSafeInteger(count) || count < 0) return state;
  const tasks: BackgroundTask[] = [];
  const seen = new Set<string>();
  if (count > 0 && Array.isArray(rawTasks)) {
    for (const task of rawTasks) {
      if (!task || typeof task.id !== 'string' || !task.id || typeof task.description !== 'string' || seen.has(task.id)) continue;
      tasks.push({ id: task.id, description: task.description, ...(task.kind === 'agent' ? { kind: 'agent' as const } : {}) });
      seen.add(task.id);
    }
  }
  if (count === state.backgroundTaskCount && JSON.stringify(tasks) === JSON.stringify(state.backgroundTasks ?? [])) return state;
  return { ...state, backgroundTaskCount: count, backgroundTasks: tasks };
}
