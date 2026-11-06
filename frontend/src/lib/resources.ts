import { fetchJSON } from './api.requests';

export interface ResourcePoint {
  timestamp: number;
  cpuPercent: number;
  memoryBytes: number;
  processes?: ResourceProcess[];
}

export interface ResourceProcess {
  pid: number;
  parentPid: number;
  name: string;
  command?: string;
  sessionId?: string;
  sessionTitle?: string;
  projectName?: string;
  cpuPercent: number;
  memoryBytes: number;
}

export interface ResourceSnapshot extends ResourcePoint {
  processes: ResourceProcess[];
  history: ResourcePoint[];
}

export function resources(signal: AbortSignal) {
  return fetchJSON<ResourceSnapshot>('/api/resources?remoteId=local', signal);
}
