import { fetchJSON, postJSON } from './api';
import type { PR, PRChecks, RemoteType, Upstream } from './upstreamApi';

export interface ForgeConnection {
  host: string; provider: RemoteType; source: string; status: string; account?: string; help: string;
}
export interface PRLink {
  platform: string; sessionId: string; remoteId: string; directory: string;
  remote: Upstream; number: number; pr: PR; watching: boolean; lastError?: string;
}
const target = (id: string) => `/api/session/${encodeURIComponent(id)}`;
const query = (platform: string) => `?platform=${encodeURIComponent(platform)}`;
export const prWorkflowApi = {
  connections: () => fetchJSON<{ connections: ForgeConnection[] }>('/api/source-control'),
  saveCredential: (host: string, provider: RemoteType, token: string) => postJSON<void>('/api/source-control/credentials', { host, provider, token }, { parseJSON: false }),
  recheck: () => postJSON<void>('/api/source-control/refresh', {}, { parseJSON: false }),
  links: (id: string, platform: string, refresh = false) => fetchJSON<{ pullRequests: PRLink[] }>(`${target(id)}/pull-requests${query(platform)}${refresh ? '&refresh=true' : ''}`),
  checks: (link: PRLink, signal: AbortSignal, refresh = false) => {
    const q = new URLSearchParams({ platform: link.platform, host: link.remote.host, repo: link.remote.repo, number: String(link.number), sha: link.pr.headSha ?? '' });
    if (refresh) q.set('refresh', 'true');
    return fetchJSON<PRChecks>(`${target(link.sessionId)}/pull-request-checks?${q}`, signal);
  },
  change: (id: string, platform: string, input: { action: string; number: number; remote?: string; host?: string; repo?: string }) => postJSON<PRLink | undefined>(`${target(id)}/pull-requests${query(platform)}`, input, { parseJSON: input.action !== 'unlink' }),
  feedback: (id: string, platform: string, link: PRLink, action: string) => postJSON<{ prompt: string }>(`${target(id)}/pull-request-feedback${query(platform)}`, { action, number: link.number, host: link.remote.host, repo: link.remote.repo }),
};
