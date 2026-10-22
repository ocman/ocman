import { fetchJSON, queryString } from './api.requests';

export type BackgroundTaskStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'interrupted';

export interface BackgroundTask {
  id: string;
  kind: string;
  key?: string;
  title: string;
  scope?: string;
  ownerId: string;
  status: BackgroundTaskStatus;
  progress: {
    phase: string;
    completed: number;
    total: number | null;
    unit?: string;
    counters: Record<string, number>;
  };
  createdAt: number;
  startedAt?: number;
  updatedAt: number;
  finishedAt?: number;
  error?: string;
}

export function backgroundTasks(status: BackgroundTaskStatus | '', signal?: AbortSignal) {
  return fetchJSON<BackgroundTask[]>(`/api/background-tasks${queryString({ status: status || undefined, limit: 200 })}`, signal);
}

export function backgroundTask(id: string, signal?: AbortSignal) {
  return fetchJSON<BackgroundTask>(`/api/background-tasks/${encodeURIComponent(id)}`, signal);
}

export function backgroundTaskCount(signal?: AbortSignal) {
  return fetchJSON<{ count: number }>('/api/background-tasks?view=active-count', signal);
}
