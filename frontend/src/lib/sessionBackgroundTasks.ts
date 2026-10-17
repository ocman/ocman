import type { SessionView } from './sessionViewTypes';

export function reduceBackgroundTaskCount(state: SessionView, count: unknown): SessionView {
  if (typeof count !== 'number' || !Number.isSafeInteger(count) || count < 0 || count === state.backgroundTaskCount) return state;
  return { ...state, backgroundTaskCount: count };
}

export function backgroundSnapshotCount(snapshot: unknown, sessionId: string): unknown {
  if (!snapshot || typeof snapshot !== 'object') return 0;
  return (snapshot as Record<string, unknown>)[sessionId] ?? 0;
}
