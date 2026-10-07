import { apiFetch, BackendUnavailableError, fetchJSON, postJSON, queryString, raiseForUnauthorized, readJSON } from './api.requests';
import type { AgentInfo, NewSessionTarget, PrepareSessionResponse, StartSessionRequest, StartSessionResponse,
  ResolveTargetsResponse, Session, NotifyEntry, SessionDetail, SessionChanges, SessionInfo, TaskSessionData,
  SessionModelsResponse, QueuedMessage, PermissionRule, SlashCommand, ShareLink, GlobalShareLink,
  SharedConversation } from './api.types';

export const sessionApi = {
  sessions: (params?: { dir?: string; since?: number; limit?: number }, signal?: AbortSignal) =>
    fetchJSON<Session[]>(`/api/sessions${queryString(params)}`, signal),
  sessionsNotify: (params?: { since?: number; limit?: number }, signal?: AbortSignal) =>
    fetchJSON<NotifyEntry[]>(`/api/sessions/notify${queryString(params)}`, signal),
  // Reads preserve archives; only a navigation fetch explicitly opens a session.
  session: (id: string, limit = 50, offset = 0, signal?: AbortSignal, platform?: string, peek = true) => {
    const query = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    if (platform) query.set('platform', platform);
    if (peek) query.set('peek', '1');
    return fetchJSON<SessionDetail>(`/api/session/${id}?${query.toString()}`, signal);
  },
  listShareLinks: (id: string, signal?: AbortSignal) =>
    fetchJSON<ShareLink[]>(`/api/session/${encodeURIComponent(id)}/shares`, signal),
  createShareLink: (id: string) =>
    postJSON<ShareLink>(`/api/session/${encodeURIComponent(id)}/share`, undefined),
  revokeShareLink: (id: string, token: string) =>
    postJSON<void>(`/api/session/${encodeURIComponent(id)}/share/${encodeURIComponent(token)}`, undefined, { method: 'DELETE', parseJSON: false }),
  listAllShares: (signal?: AbortSignal) => fetchJSON<GlobalShareLink[]>('/api/shares', signal),
  sharedConversation: (token: string, signal?: AbortSignal) =>
    fetchJSON<SharedConversation>(`/api/share/${encodeURIComponent(token)}`, signal),
  sessionChanges: (id: string, signal?: AbortSignal) =>
    fetchJSON<SessionChanges>(`/api/session/${encodeURIComponent(id)}/changes`, signal),
  sessionInfo: (id: string, signal?: AbortSignal, platform?: string) =>
    fetchJSON<SessionInfo>(`/api/session/${encodeURIComponent(id)}/info${queryString({ platform })}`, signal),
  sessionTasks: (sessionId: string, taskIds: string[], signal?: AbortSignal) =>
    fetchJSON<{ tasks: Record<string, TaskSessionData> }>(`/api/session/${encodeURIComponent(sessionId)}/tasks?ids=${taskIds.map(encodeURIComponent).join(',')}`, signal),
  archiveSession: (platform: string, sessionId: string, timeUpdated: number, archived = true) =>
    postJSON<{ ok: boolean }>('/api/session/archive', { platform, sessionId, timeUpdated, archived }),
  markSessionSeen: (platform: string, sessionId: string, timeUpdated: number) =>
    postJSON<{ ok: boolean }>('/api/session/seen', { platform, sessionId, timeUpdated }),
  pinSession: (platform: string, sessionId: string, pinned: boolean) =>
    postJSON<{ ok: boolean }>('/api/session/pin', { platform, sessionId, pinned }),
  sessionModels: (sessionId: string, platform?: string) =>
    fetchJSON<SessionModelsResponse>(`/api/session/${encodeURIComponent(sessionId)}/models${queryString({ platform })}`),
  resolveTargets: (dir: string, remoteId?: string) =>
    postJSON<ResolveTargetsResponse>('/api/sessions/resolve-targets', { dir, ...(remoteId ? { remoteId } : {}) }),
  prepareSession: (target: NewSessionTarget, signal?: AbortSignal) =>
    postJSON<PrepareSessionResponse>('/api/sessions/prepare', target, { signal }),
  startSession: (req: StartSessionRequest) => postJSON<StartSessionResponse>('/api/sessions/start', req),
  /** parentSessionId seeds the child's permission posture. */
  createSession: async (directory: string, platform?: string, title?: string, parentSessionId?: string) => {
    const resp = await apiFetch('/api/sessions', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ directory, ...(platform ? { platform } : {}), ...(title ? { title } : {}),
        ...(parentSessionId ? { parentSessionId } : {}) }),
    });
    if (!resp.ok) {
      await raiseForUnauthorized(resp);
      const body = (await resp.text()).trim();
      // 503 tags the auto-launch path for a directory without a live instance.
      if (resp.status === 503) {
        const err = new Error(body || 'No running platform instance for this directory.');
        (err as Error & { code?: string }).code = 'unreachable';
        throw err;
      }
      throw new Error(body || `HTTP ${resp.status}`);
    }
    return readJSON<{ id: string }>(resp);
  },
  sendMessage: async (
    sessionId: string, message: string, images?: { url: string; mime: string }[], model?: string,
    agent?: string, reasoning?: string, platform?: string,
    // Queueing is an explicit Ctrl/Cmd+Enter gesture, never inferred from status.
    queue?: boolean,
  ) => {
    const query = platform ? `?platform=${encodeURIComponent(platform)}` : '';
    const resp = await apiFetch(`/api/session/${encodeURIComponent(sessionId)}/message${query}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message, images, model, agent, reasoning, queue }),
    });
    if (resp.status === 204) return;
    if (resp.ok || [500, 502, 503, 504].includes(resp.status)) throw new BackendUnavailableError();
    if (!resp.ok) {
      await raiseForUnauthorized(resp);
      const body = (await resp.text()).trim();
      if (resp.status === 409) {
        const err = new Error(body || 'The session is still responding to a previous prompt. Try again in a moment.');
        (err as Error & { code?: string }).code = 'busy';
        throw err;
      }
      if (resp.status === 422 && /rate.limit|would exceed your account/i.test(body)) {
        const err = new Error(body);
        (err as Error & { code?: string }).code = 'rate_limit';
        throw err;
      }
      throw new Error(body || `HTTP ${resp.status}`);
    }
  },
  queuedMessages: async (sessionId: string, platform?: string): Promise<QueuedMessage[]> => {
    const query = platform ? `?platform=${encodeURIComponent(platform)}` : '';
    const resp = await apiFetch(`/api/session/${encodeURIComponent(sessionId)}/queue${query}`);
    await raiseForUnauthorized(resp);
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    return readJSON<QueuedMessage[]>(resp);
  },
  deleteQueuedMessage: async (sessionId: string, queuedId: string, platform?: string) => {
    const query = platform ? `?platform=${encodeURIComponent(platform)}` : '';
    const resp = await apiFetch(`/api/session/${encodeURIComponent(sessionId)}/queue/${encodeURIComponent(queuedId)}${query}`, { method: 'DELETE' });
    await raiseForUnauthorized(resp);
    if (!resp.ok && resp.status !== 404) throw new Error(`HTTP ${resp.status}`);
  },
  moveQueuedMessage: async (sessionId: string, queuedId: string, direction: -1 | 1, platform?: string) => {
    const query = platform ? `?platform=${encodeURIComponent(platform)}` : '';
    const resp = await apiFetch(`/api/session/${encodeURIComponent(sessionId)}/queue/${encodeURIComponent(queuedId)}/move${query}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ direction }),
    });
    await raiseForUnauthorized(resp);
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
  },
  uploadComposerAttachment: async (sessionId: string, file: File, platform?: string) => {
    const form = new FormData();
    form.append('file', file);
    const resp = await apiFetch(`/api/session/${encodeURIComponent(sessionId)}/attachment${queryString({ platform })}`, { method: 'POST', body: form });
    if (!resp.ok) {
      await raiseForUnauthorized(resp);
      const body = (await resp.text()).trim();
      throw new Error(body || `HTTP ${resp.status}`);
    }
    return readJSON<{ path: string; name: string; mime: string; size: number }>(resp);
  },
  listPermissions: (sessionId: string) => fetchJSON<unknown[]>(`/api/session/${encodeURIComponent(sessionId)}/permissions`),
  // Owner-pinned live read; absence means resolved.
  refreshPermissions: (sessionId: string, platform: string) =>
    fetchJSON<unknown[]>(`/api/session/${encodeURIComponent(sessionId)}/permissions${queryString({ refresh: 1, platform })}`),
  respondPermission: (sessionId: string, permissionId: string, reply: 'once' | 'always' | 'reject', platform?: string) =>
    postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/permissions/${encodeURIComponent(permissionId)}${queryString({ platform })}`, { reply }, { parseJSON: false }),
  getPermissionRules: (sessionId: string) => fetchJSON<{ rules: PermissionRule[] }>(`/api/session/${encodeURIComponent(sessionId)}/permission-rules`),
  setPermissionRules: (sessionId: string, rules: PermissionRule[]) =>
    postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/permission-rules`, { rules }, { method: 'PUT', parseJSON: false }),
  listQuestions: (sessionId: string) => fetchJSON<unknown[]>(`/api/session/${encodeURIComponent(sessionId)}/questions`),
  respondQuestion: (sessionId: string, requestId: string, answers: string[][]) =>
    postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/questions/${encodeURIComponent(requestId)}`, { answers }, { parseJSON: false }),
  rejectQuestion: (sessionId: string, requestId: string) =>
    postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/questions/${encodeURIComponent(requestId)}/reject`, undefined, { parseJSON: false }),
  abortSession: (sessionId: string) => postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/abort`, undefined, { parseJSON: false }),
  revertSession: (sessionId: string, messageID: string) => postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/revert`, { messageID }, { parseJSON: false }),
  unrevertSession: (sessionId: string) => postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/unrevert`, undefined, { parseJSON: false }),
  compactSession: (sessionId: string, providerID: string, modelID: string) =>
    postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/compact`, { providerID, modelID }, { parseJSON: false }),
  forkSession: (sessionId: string, messageID?: string) => postJSON<{ id: string }>(`/api/session/${encodeURIComponent(sessionId)}/fork`, { messageID: messageID ?? '' }),
  moveSession: (sessionId: string, directory: string) => postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/move`, { directory }, { parseJSON: false }),
  commands: (sessionId: string, signal?: AbortSignal) => fetchJSON<SlashCommand[]>(`/api/session/${encodeURIComponent(sessionId)}/commands`, signal),
  agents: (sessionId: string, signal?: AbortSignal, platform?: string) =>
    fetchJSON<AgentInfo[]>(`/api/session/${encodeURIComponent(sessionId)}/agents${queryString({ platform })}`, signal),
  executeCommand: (sessionId: string, command: string, args: string, model?: string, agent?: string) =>
    postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/command`, { command, arguments: args, model, agent }, { parseJSON: false }),
  restartOpencode: (sessionId: string, options: { all?: boolean; force?: boolean; confirmed?: boolean } = {}): Promise<{ restarted?: number; confirmationRequired?: boolean; busySessions?: string[] }> => {
    const query = new URLSearchParams();
    if (options.all) query.set('all', 'true');
    if (options.force) query.set('force', 'true');
    if (options.confirmed) query.set('confirmed', 'true');
    const suffix = query.size ? `?${query}` : '';
    return postJSON(`/api/session/${encodeURIComponent(sessionId)}/restart-opencode${suffix}`, undefined);
  },
  reloadOpencode: (sessionId: string, platform: string): Promise<void> =>
    postJSON(`/api/session/${encodeURIComponent(sessionId)}/reload-opencode${queryString({ platform })}`, undefined),
  runShell: (sessionId: string, command: string, agent?: string) =>
    postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/shell`, { command, agent }, { parseJSON: false }),
  renameSession: (sessionId: string, title: string) =>
    postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}`, { title }, { method: 'PATCH', parseJSON: false }),
  approvedPermissions: (sessionId: string) =>
    fetchJSON<Array<{ permissionId: string; permission: string; patterns: string[]; reasoning: string; approvedAt: number }>>(`/api/session/${encodeURIComponent(sessionId)}/approved-permissions`),
  getAutoApprove: (sessionId: string) => fetchJSON<{ enabled: boolean; overridden: boolean }>(`/api/session/${encodeURIComponent(sessionId)}/auto-approve`),
  setAutoApprove: (sessionId: string, enabled: boolean): Promise<void> =>
    postJSON<void>(`/api/session/${encodeURIComponent(sessionId)}/auto-approve`, { enabled }, { parseJSON: false }),
};
