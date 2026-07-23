import { fetchJSON } from './api';

export type BeadsTicket = {
  id: string;
  title: string;
  status: 'open' | 'in_progress' | 'blocked' | 'deferred';
  priority: number;
  issueType?: string;
  parentId?: string;
};

export type BeadsStatus = {
  available: boolean;
  tickets?: BeadsTicket[];
  error?: 'status_unavailable' | 'unsupported_schema';
};

export function fetchBeadsStatus(directory: string, remoteId: string, signal?: AbortSignal): Promise<BeadsStatus> {
  const params = new URLSearchParams({ dir: directory, remoteId });
  return fetchJSON<BeadsStatus>(`/api/project/beads-status?${params}`, signal);
}
