import { apiFetch, fetchJSON, postJSON, queryString, raiseForUnauthorized, readJSON } from './api.requests';
import type { DirectoryBrowseResponse, DirectorySearchResponse, WorkingTreeDiff, RepoFileList, RepoFileContent,
  RemoteAccessStatus, RemoteStatus, TmuxClient, TmuxSession, TermWindow, WorktreeEntry,
  WorktreeCreateRequest, WorktreeCreateResponse, WorktreeRemoveRequest } from './api.types';

const ownerParam = (remoteId?: string) => (remoteId ? `&remoteId=${encodeURIComponent(remoteId)}` : '');

export const hostApi = {
  browseDirectories: (dir?: string, signal?: AbortSignal) =>
    fetchJSON<DirectoryBrowseResponse>(`/api/filesystem/directories${queryString({ dir })}`, signal),
  searchDirectories: (root: string | undefined, query: string, limit?: number, signal?: AbortSignal) => {
    const q = new URLSearchParams({ q: query });
    if (root) q.set('root', root);
    if (limit) q.set('limit', String(limit));
    return fetchJSON<DirectorySearchResponse>(`/api/filesystem/directory-search?${q.toString()}`, signal);
  },
  gitDiff: (dir: string, opts?: { fresh?: boolean }, signal?: AbortSignal) => {
    const q = new URLSearchParams({ dir });
    if (opts?.fresh) q.set('fresh', '1');
    return fetchJSON<WorkingTreeDiff>(`/api/git/diff?${q.toString()}`, signal);
  },
  repoFiles: (dir: string, remoteId: string, signal?: AbortSignal, ignored = false) =>
    fetchJSON<RepoFileList>(`/api/git/files?dir=${encodeURIComponent(dir)}&remoteId=${encodeURIComponent(remoteId)}${ignored ? '&ignored=1' : ''}`, signal),
  repoFile: (dir: string, path: string, remoteId: string, signal?: AbortSignal, ignored = false) =>
    fetchJSON<RepoFileContent>(`/api/git/file?dir=${encodeURIComponent(dir)}&path=${encodeURIComponent(path)}&remoteId=${encodeURIComponent(remoteId)}${ignored ? '&ignored=1' : ''}`, signal),
  gitBranches: (dir: string, signal?: AbortSignal) => fetchJSON<{ branches: string[] }>(`/api/git/branches?dir=${encodeURIComponent(dir)}`, signal),
  gitCheckout: (dir: string, branch: string) => postJSON<{ branch: string }>('/api/git/checkout', { dir, branch }),
  remoteAccess: (signal?: AbortSignal) => fetchJSON<RemoteAccessStatus>('/api/settings/remote-access', signal),
  revealRemoteToken: () => postJSON<{ token: string }>('/api/settings/remote-access/reveal-token', undefined),
  listRemotes: (signal?: AbortSignal) => fetchJSON<RemoteStatus[]>('/api/remotes', signal),
  addRemote: (body: { address: string; token: string; displayName?: string }) => postJSON<RemoteStatus>('/api/remotes', body),
  updateRemote: (localId: number, body: { address: string; displayName: string; enabled: boolean; token?: string | null }) =>
    postJSON<{ ok: boolean }>(`/api/remotes/${localId}`, body, { method: 'PUT' }),
  removeRemote: (localId: number) => postJSON<{ ok: boolean }>(`/api/remotes/${localId}`, undefined, { method: 'DELETE', parseJSON: false }),
  reconnectRemote: (localId: number) => postJSON<{ ok: boolean }>(`/api/remotes/${localId}/reconnect`, undefined),
  tmuxClients: (signal?: AbortSignal) => fetchJSON<{ available: boolean; clients: TmuxClient[] }>('/api/tmux/clients', signal),
  tmuxSessions: (signal?: AbortSignal) => fetchJSON<{ available: boolean; sessions: TmuxSession[] }>('/api/tmux/sessions', signal),
  tmuxSwitch: (session: string, client?: string) => {
    const body: Record<string, string> = { session };
    if (client) body.client = client;
    return postJSON<void>('/api/tmux/switch', body, { parseJSON: false });
  },
  tmuxLaunchOpencode: (directory: string, remoteId?: string): Promise<{ session: string }> =>
    postJSON<{ session: string }>('/api/tmux/launch-opencode', { directory, ...(remoteId ? { remoteId } : {}) }),
  term: {
    listWindows: (dir: string, remoteId?: string, signal?: AbortSignal) => {
      const params = new URLSearchParams({ dir });
      if (remoteId && remoteId !== 'local') params.set('remoteId', remoteId);
      return fetchJSON<{ windows: TermWindow[] }>(`/api/term/windows?${params.toString()}`, signal);
    },
    createWindow: async (dir: string, remoteId?: string): Promise<{ window: string }> => {
      const resp = await apiFetch('/api/term/windows', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dir, ...(remoteId && remoteId !== 'local' ? { remoteId } : {}) }),
      });
      await raiseForUnauthorized(resp);
      if (!resp.ok) throw new Error(await resp.text());
      return readJSON(resp);
    },
    killWindow: async (dir: string, window: string, remoteId?: string): Promise<void> => {
      const resp = await apiFetch('/api/term/windows', {
        method: 'DELETE', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dir, window, ...(remoteId && remoteId !== 'local' ? { remoteId } : {}) }),
      });
      await raiseForUnauthorized(resp);
      if (!resp.ok) throw new Error(await resp.text());
    },
  },
  worktree: {
    // Explicit ownership fails closed when the remote is disconnected.
    list: (dir: string, remoteId?: string, signal?: AbortSignal) =>
      fetchJSON<{ worktrees: WorktreeEntry[] }>(`/api/worktree/list?dir=${encodeURIComponent(dir)}${ownerParam(remoteId)}`, signal),
    defaultBaseRef: (dir: string, remoteId?: string, signal?: AbortSignal) =>
      fetchJSON<{ baseRef: string }>(`/api/worktree/default-base-ref?dir=${encodeURIComponent(dir)}${ownerParam(remoteId)}`, signal),
    createAndLaunch: (req: WorktreeCreateRequest): Promise<WorktreeCreateResponse> => postJSON<WorktreeCreateResponse>('/api/worktree/create-and-launch', req),
    remove: (req: WorktreeRemoveRequest): Promise<{ removed: boolean }> => postJSON<{ removed: boolean }>('/api/worktree/remove', req),
  },
  whisperStatus: (signal?: AbortSignal) => fetchJSON<{ available: boolean }>('/api/whisper/status', signal),
  transcribe: async (audio: Blob): Promise<string> => {
    const extMap: Record<string, string> = {
      'audio/webm': '.webm', 'audio/webm;codecs=opus': '.webm', 'audio/ogg': '.ogg',
      'audio/ogg;codecs=opus': '.ogg', 'audio/mp4': '.m4a', 'audio/wav': '.wav', 'audio/x-wav': '.wav',
    };
    const ext = extMap[audio.type] || '.webm';
    const form = new FormData();
    form.append('audio', audio, `recording${ext}`);
    const resp = await apiFetch('/api/transcribe', { method: 'POST', body: form });
    await raiseForUnauthorized(resp);
    if (!resp.ok) throw new Error(await resp.text());
    const data = await readJSON<{ text: string }>(resp);
    return data.text;
  },
};
