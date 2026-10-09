import type { PermissionRule } from './api.types';

export type RoutineScheduleKind = 'none' | 'timeout' | 'once' | 'cron';
export type RoutineSessionMode = 'new' | 'reuse' | 'existing';

export interface Routine {
  id: string;
  name: string;
  prompt: string;
  directory: string;
  remoteId: string;
  agent: string;
  model: string;
  sessionMode: RoutineSessionMode;
  sessionId: string;
  worktree?: boolean;
  cleanupWorktree?: boolean;
  scheduleKind: RoutineScheduleKind;
  scheduleConfigJSON: string;
  permissionRulesJSON: string;
  nextDueAt: number;
  enabled: boolean;
  deleted: boolean;
  deleteAfterSuccess: boolean;
  archiveSessionAfterSuccess: boolean;
  notifyOnSuccess: boolean;
  createdAt: number;
  updatedAt: number;
  deletedAt?: number;
  expiredAt?: number;
  /** Newest run, attached by the list endpoint. */
  latestRun?: RoutineRun;
}

export interface RoutineRun {
  id: string;
  routineId: string;
  routineUpdatedAt: number;
  routineName: string;
  prompt: string;
  directory: string;
  remoteId: string;
  agent: string;
  model: string;
  sessionMode: RoutineSessionMode;
  targetSessionId: string;
  worktree?: boolean;
  cleanupWorktree?: boolean;
  worktreePath?: string;
  trigger: 'manual' | 'schedule';
  platform?: string;
  sessionId?: string;
  state: 'running' | 'success' | 'failure' | 'interrupted';
  error?: string;
  occurrenceAt: number;
  createdAt: number;
  startedAt?: number;
  finishedAt?: number;
}

export interface RoutineInput {
  name: string;
  prompt: string;
  directory: string;
  remoteId: string;
  agent: string;
  model: string;
  sessionMode: RoutineSessionMode;
  sessionId: string;
  worktree?: boolean;
  cleanupWorktree?: boolean;
  schedule: { kind: RoutineScheduleKind; timeoutMs?: number; at?: number; cron?: string; timezone?: string };
  enabled: boolean;
  deleteAfterSuccess: boolean;
  archiveSessionAfterSuccess: boolean;
  notifyOnSuccess: boolean;
  permissionRules: PermissionRule[];
}
