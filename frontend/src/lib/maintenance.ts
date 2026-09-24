import { fetchJSON, postJSON } from './api';

export interface MaintenanceStep {
  name: string;
  state: 'pending' | 'running' | 'done' | 'failed' | 'skipped';
  detail?: string;
}

export interface MaintenanceStatus {
  available: boolean;
  dbPath?: string;
  dbBytes: number;
  dumpPath?: string;
  dumpBytes: number;
  cutoffDays: number;
  job: {
    job?: 'cleanup' | 'restore';
    running: boolean;
    steps: MaintenanceStep[];
    error?: string;
    startedAt?: string;
    finishedAt?: string;
  };
}

const base = '/api/maintenance/opencode-db';

export const maintenance = {
  status: (signal?: AbortSignal) => fetchJSON<MaintenanceStatus>(base, signal),
  cleanup: () => postJSON<MaintenanceStatus>(`${base}/cleanup`, undefined),
  restore: () => postJSON<MaintenanceStatus>(`${base}/restore`, undefined),
  deleteDump: () => postJSON<MaintenanceStatus>(`${base}/delete-dump`, undefined),
};
