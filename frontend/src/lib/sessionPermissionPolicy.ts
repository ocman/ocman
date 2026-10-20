import { fetchJSON, postJSON, queryString } from './api.requests';
import type { PermissionRule } from './api.types';

export interface PermissionHistoryEntry {
  platform: string;
  sessionId: string;
  permissionId: string;
  permission: string;
  patterns: string[];
  requestedAt: number;
  resolvedAt?: number;
  resolution: string;
}

export interface SessionPermissionPolicy {
  rules: PermissionRule[];
  requests: PermissionHistoryEntry[];
}

const path = (id: string, platform: string) => `/api/session/${encodeURIComponent(id)}/permission-policy${queryString({ platform })}`;

export const sessionPermissionPolicy = {
  get: (id: string, platform: string) => fetchJSON<SessionPermissionPolicy>(path(id, platform)),
  save: (id: string, platform: string, rules: PermissionRule[]) =>
    postJSON<SessionPermissionPolicy>(path(id, platform), { rules }, { method: 'PUT' }),
};
