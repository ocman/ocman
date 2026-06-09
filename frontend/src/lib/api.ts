import { record as recordPerf, templatePath } from './perfRing';

// Re-export every wire type from the dedicated types module so existing
// imports of `'./api'` continue to work unchanged. New code can import
// from `./api.types` directly.
export type {
  NotifyEntry,
  Session,
  GitInfo,
  Message,
  Part,
  FilePart,
  PartData,
  SessionDetail,
  TaskSessionData,
  SessionEdit,
  FileChange,
  WorkingTreeFile,
  WorkingTreeDiff,
  SessionChanges,
  MCPServer,
  LSPServer,
  SessionInfoContext,
  SessionInfoTokens,
  SessionInfoTodo,
  SessionInfoMessages,
  SessionInfo,
  Stats,
  MetricsSummary,
  MetricsPoint,
  StopReasonCount,
  RequestMetricsRow,
  SessionLogEntry,
  ProjectLogEntry,
  MetricsDashboard,
  MetricsCostByModel,
  ModelCostPoint,
  Project,
  ActivityDay,
  ModelUsage,
  HourlyData,
  HourlyTokensByModel,
  PlatformCapabilities,
  PlatformCapabilityEntry,
  CapabilitiesResponse,
  WorktreeEntry,
  WorktreeCreateRequest,
  WorktreeCreateResponse,
  SlashCommand,
  SessionModelEntry,
  FavoriteEntry,
  SessionModelsResponse,
  AgentInfo,
  TmuxClient,
  TmuxSession,
  TermWindow,
  AuthMe,
  SystemStats,
  SessionNotice,
  ShareLink,
  SharedConversation,
} from './api.types';

// Type imports used by the api object below.
import type {
  AuthMe,
  CapabilitiesResponse,
  FavoriteEntry,
  MetricsDashboard,
  ModelUsage,
  NotifyEntry,
  Project,
  Session,
  SessionChanges,
  SessionDetail,
  SessionInfo,
  SessionModelsResponse,
  SlashCommand,
  Stats,
  SystemStats,
  TaskSessionData,
  TmuxClient,
  TmuxSession,
  TermWindow,
  WorktreeCreateRequest,
  WorktreeCreateResponse,
  WorktreeEntry,
  WorkingTreeDiff,
  AgentInfo,
  ActivityDay,
  HourlyData,
  HourlyTokensByModel,
  ShareLink,
  SharedConversation,
} from './api.types';

/**
 * Absolute URL for downloading a session's conversation as Markdown.
 * Used directly as an <a href> / download target. Auth-gated (the
 * browser sends the auth cookie automatically).
 */
export function sessionExportMarkdownUrl(id: string): string {
  return `/api/session/${encodeURIComponent(id)}/export.md`;
}

/**
 * Absolute URL for the public Markdown export of a shared conversation.
 * Unauthenticated — usable from the read-only share page.
 */
export function sharedExportMarkdownUrl(token: string): string {
  return `/api/share/${encodeURIComponent(token)}/export.md`;
}

/**
 * AuthError is thrown when the backend reports that the client is
 * unauthenticated (HTTP 401). It's a distinct error type so callers —
 * and in particular the global fetch wrappers — can fan out into the
 * lockscreen flow rather than surfacing an opaque "unauthorized"
 * message in a red banner.
 *
 * The `authStore.handleAuthError` hook (installed at app boot) sees
 * instances of this class and flips the authenticated flag so the
 * router re-renders the login page.
 */
export class AuthError extends Error {
  constructor(message = 'unauthorized') {
    super(message);
    this.name = 'AuthError';
  }
}

// Pluggable 401 hook. authStore installs itself here at boot so it
// doesn't need to live inside api.ts itself (would be an import cycle).
// The default is a no-op; replacing it is explicit opt-in.
type AuthErrorHandler = (err: AuthError) => void;
let onAuthError: AuthErrorHandler = () => {};

/**
 * registerAuthErrorHandler installs a callback that runs whenever any
 * call through fetchJSON / postJSON observes a 401. The callback
 * should not re-throw; it's fire-and-forget state plumbing.
 * Returns the previous handler so callers can chain or restore.
 */
export function registerAuthErrorHandler(handler: AuthErrorHandler): AuthErrorHandler {
  const previous = onAuthError;
  onAuthError = handler;
  return previous;
}

// Internal: surface a 401 as an AuthError and notify the registered
// handler. Callers that catch this will receive an AuthError; anyone
// who doesn't catch still lets the handler update global auth state.
async function throwForStatus(resp: Response): Promise<never> {
  if (resp.status === 401) {
    const err = new AuthError(await resp.text().catch(() => 'unauthorized'));
    onAuthError(err);
    throw err;
  }
  throw new Error(await resp.text());
}

export async function fetchJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const startedAt = performance.now();
  let status = 0;
  try {
    const resp = await fetch(url, signal ? { signal } : undefined);
    status = resp.status;
    if (!resp.ok) await throwForStatus(resp);
    return await resp.json();
  } finally {
    // Record after the JSON parse so durationMs reflects the user-
    // visible cost, not just the HTTP round-trip. Errors and aborts
    // also land here (status=0 for aborts, or the real status for
    // a non-2xx that threw via throwForStatus).
    recordPerf({
      pathTemplate: templatePath(url),
      method: 'GET',
      status,
      durationMs: performance.now() - startedAt,
      startedAt,
    });
  }
}

/**
 * postJSON is the mutation counterpart to fetchJSON: JSON body in,
 * JSON body out, with identical 401 handling and perf recording. Use
 * it for every mutating call so they all participate in the AuthError
 * fan-out and the perf ring.
 *
 * Options:
 * - `method`: override the HTTP verb (defaults to POST; pass DELETE /
 *   PATCH as needed).
 * - `body`: omit (pass `undefined`) for verb-only requests that send
 *   no JSON body; the Content-Type header is then dropped too.
 * - `parseJSON`: set false when the server returns 204 No Content
 *   (login returns a body, but logout doesn't).
 */
export async function postJSON<TResp, TReq = unknown>(
  url: string,
  body: TReq,
  opts?: { signal?: AbortSignal; parseJSON?: boolean; method?: 'POST' | 'PATCH' | 'DELETE' },
): Promise<TResp> {
  const method = opts?.method ?? 'POST';
  const startedAt = performance.now();
  let status = 0;
  try {
    const hasBody = body !== undefined;
    const resp = await fetch(url, {
      method,
      headers: hasBody ? { 'Content-Type': 'application/json' } : undefined,
      body: hasBody ? JSON.stringify(body) : undefined,
      signal: opts?.signal,
    });
    status = resp.status;
    if (!resp.ok) await throwForStatus(resp);
    if (opts?.parseJSON === false || resp.status === 204) {
      return undefined as unknown as TResp;
    }
    return await resp.json();
  } finally {
    recordPerf({
      pathTemplate: templatePath(url),
      method,
      status,
      durationMs: performance.now() - startedAt,
      startedAt,
    });
  }
}


export const api = {
  stats: (signal?: AbortSignal) => fetchJSON<Stats>('/api/stats', signal),
  metrics: (params?: { agent?: string; model?: string; days?: number; limit?: number; offset?: number; sessionLimit?: number; sessionOffset?: number; projectLimit?: number; projectOffset?: number; dir?: string }, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (params?.agent) q.set('agent', params.agent);
    if (params?.model) q.set('model', params.model);
    if (params?.days != null) q.set('days', String(params.days));
    if (params?.limit != null) q.set('limit', String(params.limit));
    if (params?.offset != null) q.set('offset', String(params.offset));
    if (params?.sessionLimit != null) q.set('sessionLimit', String(params.sessionLimit));
    if (params?.sessionOffset != null) q.set('sessionOffset', String(params.sessionOffset));
    if (params?.projectLimit != null) q.set('projectLimit', String(params.projectLimit));
    if (params?.projectOffset != null) q.set('projectOffset', String(params.projectOffset));
    if (params?.dir) q.set('dir', params.dir);
    const qs = q.toString();
    return fetchJSON<MetricsDashboard>(`/api/metrics${qs ? '?' + qs : ''}`, signal);
  },
  projects: (signal?: AbortSignal) => fetchJSON<Project[]>('/api/projects', signal),
  sessions: (params?: { dir?: string; since?: number; limit?: number }, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (params?.dir) q.set('dir', params.dir);
    if (params?.since) q.set('since', String(params.since));
    if (params?.limit) q.set('limit', String(params.limit));
    const qs = q.toString();
    return fetchJSON<Session[]>(`/api/sessions${qs ? '?' + qs : ''}`, signal);
  },
  sessionsNotify: (params?: { since?: number; limit?: number }, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (params?.since) q.set('since', String(params.since));
    if (params?.limit) q.set('limit', String(params.limit));
    const qs = q.toString();
    return fetchJSON<NotifyEntry[]>(`/api/sessions/notify${qs ? '?' + qs : ''}`, signal);
  },
  session: (id: string, limit = 50, offset = 0, signal?: AbortSignal) => fetchJSON<SessionDetail>(`/api/session/${id}?limit=${limit}&offset=${offset}`, signal),
  // --- Conversation export / share ---
  // List the active public share links for a session.
  listShareLinks: (id: string, signal?: AbortSignal) =>
    fetchJSON<ShareLink[]>(`/api/session/${encodeURIComponent(id)}/shares`, signal),
  // Mint a new public, read-only share link for a session.
  createShareLink: (id: string) =>
    postJSON<ShareLink>(`/api/session/${encodeURIComponent(id)}/share`, undefined),
  // Revoke a previously created share link.
  revokeShareLink: (id: string, token: string) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(id)}/share/${encodeURIComponent(token)}`,
      undefined,
      { method: 'DELETE', parseJSON: false },
    ),
  // Fetch a shared conversation by token. UNAUTHENTICATED endpoint:
  // the token is the only credential. Used by the public /share/:token
  // page.
  sharedConversation: (token: string, signal?: AbortSignal) =>
    fetchJSON<SharedConversation>(`/api/share/${encodeURIComponent(token)}`, signal),
  // Aggregated per-file change summary for a session. Returns
  // supported=false (with HTTP 200) when the owning platform doesn't
  // implement aggregation (Claude Code today).
  sessionChanges: (id: string, signal?: AbortSignal) =>
    fetchJSON<SessionChanges>(`/api/session/${encodeURIComponent(id)}/changes`, signal),
  // Per-session info snapshot consumed by the right-hand "Session info"
  // panel: context-window usage, configured MCP servers and their
  // status, configured LSP servers and their status. Returns
  // supported=false (HTTP 200) when the owning platform can't produce
  // a meaningful snapshot (Claude Code today, or OpenCode without a
  // live port).
  sessionInfo: (id: string, signal?: AbortSignal) =>
    fetchJSON<SessionInfo>(`/api/session/${encodeURIComponent(id)}/info`, signal),
  /**
   * Batch-fetch sub-session data for multiple task sessions. Returns
   * messages + parts per task so the frontend can render an embedded
   * thread preview inside Task tool cards.
   */
  sessionTasks: (sessionId: string, taskIds: string[], signal?: AbortSignal) =>
    fetchJSON<{ tasks: Record<string, TaskSessionData> }>(
      `/api/session/${encodeURIComponent(sessionId)}/tasks?ids=${taskIds.map(encodeURIComponent).join(',')}`,
      signal,
    ),
  // Working-tree git diff for an absolute directory. fresh=1 bypasses
  // the backend's tiny in-process cache; the SSE-driven refetch path
  // sets it so an edit-event-triggered refresh is never stale.
  gitDiff: (dir: string, opts?: { fresh?: boolean }, signal?: AbortSignal) => {
    const q = new URLSearchParams({ dir });
    if (opts?.fresh) q.set('fresh', '1');
    return fetchJSON<WorkingTreeDiff>(`/api/git/diff?${q.toString()}`, signal);
  },
  archiveSession: (platform: string, sessionId: string, timeUpdated: number, archived = true) =>
    postJSON<{ ok: boolean }>('/api/session/archive', { platform, sessionId, timeUpdated, archived }),
  markSessionSeen: (platform: string, sessionId: string, timeUpdated: number) =>
    postJSON<{ ok: boolean }>('/api/session/seen', { platform, sessionId, timeUpdated }),
  pinSession: (platform: string, sessionId: string, pinned: boolean) =>
    postJSON<{ ok: boolean }>('/api/session/pin', { platform, sessionId, pinned }),
  calcCost: (req: { modelID: string; input: number; output: number; cacheRead: number; cacheWrite: number }) =>
    postJSON<{ cost: number; known: boolean }>('/api/cost/calc', req),
  activity: (params?: { days?: number; model?: string; dir?: string }, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (params?.days) q.set('days', String(params.days));
    if (params?.model) q.set('model', params.model);
    if (params?.dir) q.set('dir', params.dir);
    const qs = q.toString();
    return fetchJSON<ActivityDay[]>(`/api/activity${qs ? '?' + qs : ''}`, signal);
  },
  models: (params?: { days?: number; dir?: string }, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (params?.days) q.set('days', String(params.days));
    if (params?.dir) q.set('dir', params.dir);
    const qs = q.toString();
    return fetchJSON<ModelUsage[]>(`/api/models${qs ? '?' + qs : ''}`, signal);
  },
  sessionModels: (sessionId: string) =>
    fetchJSON<SessionModelsResponse>(`/api/session/${encodeURIComponent(sessionId)}/models`),
  // Favorites CRUD. Scoped per-platform because the same (provider,
  // model) pair can legitimately be a favorite under one platform but
  // not another — matches the DB's composite key.
  listFavorites: (platform: string) =>
    fetchJSON<FavoriteEntry[]>(`/api/favorites?platform=${encodeURIComponent(platform)}`),
  addFavorite: (platform: string, provider: string, model: string) =>
    postJSON<void>('/api/favorites', { platform, provider, model }, { parseJSON: false }),
  removeFavorite: (platform: string, provider: string, model: string) =>
    postJSON<void>('/api/favorites', { platform, provider, model }, { method: 'DELETE', parseJSON: false }),
  hourly: (params?: { days?: number; dir?: string }, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (params?.days) q.set('days', String(params.days));
    if (params?.dir) q.set('dir', params.dir);
    const qs = q.toString();
    return fetchJSON<HourlyData[]>(`/api/hourly${qs ? '?' + qs : ''}`, signal);
  },
  hourlyTokens: (params?: { days?: number; model?: string; dir?: string }, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (params?.days) q.set('days', String(params.days));
    if (params?.model) q.set('model', params.model);
    if (params?.dir) q.set('dir', params.dir);
    const qs = q.toString();
    return fetchJSON<HourlyTokensByModel[]>(`/api/hourly-tokens${qs ? '?' + qs : ''}`, signal);
  },
  capabilities: (signal?: AbortSignal) => fetchJSON<CapabilitiesResponse>('/api/capabilities', signal),
  createSession: async (directory: string, platform?: string, title?: string) => {
    const resp = await fetch('/api/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        directory,
        ...(platform ? { platform } : {}),
        ...(title ? { title } : {}),
      }),
    });
    if (!resp.ok) {
      const body = (await resp.text()).trim();
      // 503 maps to platforms.ErrPlatformUnreachable on the backend —
      // the directory is known but no live instance is running. Tag
      // the error so callers (SessionDetail, CommandPalette) can
      // trigger the auto-launch-in-tmux flow.
      if (resp.status === 503) {
        const err = new Error(body || 'No running platform instance for this directory.');
        (err as Error & { code?: string }).code = 'unreachable';
        throw err;
      }
      throw new Error(body || `HTTP ${resp.status}`);
    }
    return resp.json() as Promise<{ id: string }>;
  },
  sendMessage: async (
    sessionId: string,
    message: string,
    images?: { url: string; mime: string }[],
    model?: string,
    agent?: string,
    reasoning?: string,
  ) => {
    const resp = await fetch(`/api/session/${encodeURIComponent(sessionId)}/message`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message, images, model, agent, reasoning }),
    });
    if (!resp.ok) {
      const body = (await resp.text()).trim();
      // 409 Conflict is AD-13's busy-guard: the target session is
      // mid-turn and accepting this prompt would fork its history.
      // Surface a friendlier message and tag the error so callers
      // can render a distinct UI if they want.
      if (resp.status === 409) {
        const err = new Error(
          body || 'The session is still responding to a previous prompt. Try again in a moment.',
        );
        (err as Error & { code?: string }).code = 'busy';
        throw err;
      }
      // 422 Unprocessable Entity carries upstream-rejected errors.
      // When the body matches a rate-limit pattern, tag the error so
      // the failed-send banner can show rate-limit-specific copy.
      if (resp.status === 422 && /rate.limit|would exceed your account/i.test(body)) {
        const err = new Error(body);
        (err as Error & { code?: string }).code = 'rate_limit';
        throw err;
      }
      throw new Error(body || `HTTP ${resp.status}`);
    }
  },
  uploadComposerAttachment: async (sessionId: string, file: File) => {
    const form = new FormData();
    form.append('file', file);
    const resp = await fetch(`/api/session/${encodeURIComponent(sessionId)}/attachment`, {
      method: 'POST',
      body: form,
    });
    if (!resp.ok) {
      const body = (await resp.text()).trim();
      throw new Error(body || `HTTP ${resp.status}`);
    }
    return resp.json() as Promise<{ path: string; name: string; mime: string; size: number }>;
  },
  listPermissions: (sessionId: string) =>
    fetchJSON<unknown[]>(`/api/session/${encodeURIComponent(sessionId)}/permissions`),
  respondPermission: (
    sessionId: string,
    permissionId: string,
    reply: 'once' | 'always' | 'reject',
  ) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/permissions/${encodeURIComponent(permissionId)}`,
      { reply },
      { parseJSON: false },
    ),
  listQuestions: (sessionId: string) =>
    fetchJSON<unknown[]>(`/api/session/${encodeURIComponent(sessionId)}/questions`),
  respondQuestion: (
    sessionId: string,
    requestId: string,
    answers: string[][],
  ) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/questions/${encodeURIComponent(requestId)}`,
      { answers },
      { parseJSON: false },
    ),
  rejectQuestion: (sessionId: string, requestId: string) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/questions/${encodeURIComponent(requestId)}/reject`,
      undefined,
      { parseJSON: false },
    ),
  abortSession: (sessionId: string) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/abort`,
      undefined,
      { parseJSON: false },
    ),
  tmuxClients: (signal?: AbortSignal) => fetchJSON<{ available: boolean; clients: TmuxClient[] }>('/api/tmux/clients', signal),
  tmuxSessions: (signal?: AbortSignal) => fetchJSON<{ available: boolean; sessions: TmuxSession[] }>('/api/tmux/sessions', signal),
  tmuxSwitch: (session: string, client?: string) => {
    const body: Record<string, string> = { session };
    if (client) body.client = client;
    return postJSON<void>('/api/tmux/switch', body, { parseJSON: false });
  },
  tmuxLaunchOpencode: (directory: string): Promise<{ session: string }> =>
    postJSON<{ session: string }>('/api/tmux/launch-opencode', { directory }),
  /**
   * In-app terminal windows. Each entry is a dedicated tmux window
   * (`ocman-term-<slug>-<n>`) backing a terminal tab in the UI.
   */
  term: {
    listWindows: (dir: string, signal?: AbortSignal) =>
      fetchJSON<{ windows: TermWindow[] }>(
        `/api/term/windows?dir=${encodeURIComponent(dir)}`,
        signal,
      ),
    createWindow: async (dir: string): Promise<{ window: string }> => {
      const resp = await fetch('/api/term/windows', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dir }),
      });
      if (!resp.ok) throw new Error(await resp.text());
      return resp.json();
    },
    killWindow: async (dir: string, window: string): Promise<void> => {
      const resp = await fetch('/api/term/windows', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dir, window }),
      });
      if (!resp.ok) throw new Error(await resp.text());
    },
  },
  /**
   * Worktree-sessions endpoints (the /wt flow). See
   * spec/worktree-sessions/architecture.md for the design.
   */
  worktree: {
    list: (dir: string, signal?: AbortSignal) =>
      fetchJSON<{ worktrees: WorktreeEntry[] }>(
        `/api/worktree/list?dir=${encodeURIComponent(dir)}`,
        signal,
      ),
    defaultBaseRef: (dir: string, signal?: AbortSignal) =>
      fetchJSON<{ baseRef: string }>(
        `/api/worktree/default-base-ref?dir=${encodeURIComponent(dir)}`,
        signal,
      ),
    createAndLaunch: (req: WorktreeCreateRequest): Promise<WorktreeCreateResponse> =>
      postJSON<WorktreeCreateResponse>('/api/worktree/create-and-launch', req),
  },
  compactSession: (sessionId: string, providerID: string, modelID: string) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/compact`,
      { providerID, modelID },
      { parseJSON: false },
    ),
  commands: (sessionId: string, signal?: AbortSignal) =>
    fetchJSON<SlashCommand[]>(`/api/session/${encodeURIComponent(sessionId)}/commands`, signal),
  agents: (sessionId: string, signal?: AbortSignal) =>
    fetchJSON<AgentInfo[]>(`/api/session/${encodeURIComponent(sessionId)}/agents`, signal),
  executeCommand: (
    sessionId: string,
    command: string,
    args: string,
    model?: string,
    agent?: string,
  ) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/command`,
      { command, arguments: args, model, agent },
      { parseJSON: false },
    ),
  /**
   * Run a raw shell command in the session's working directory,
   * bypassing the LLM. Backed by the platform's shell-tool primitive
   * (OpenCode: POST /session/{id}/shell). Used by the composer to
   * route `!`-prefixed input on platforms that report
   * caps.shellExec. The backend defaults `agent` to "build" when
   * blank.
   */
  runShell: (sessionId: string, command: string, agent?: string) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/shell`,
      { command, agent },
      { parseJSON: false },
    ),
  renameSession: (sessionId: string, title: string) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}`,
      { title },
      { method: 'PATCH', parseJSON: false },
    ),
  // Best-effort remote log. Used by remoteLog.* to ship debug output to the
  // backend when browser devtools aren't reachable (e.g. on iPad). Errors
  // are swallowed so a failing log call never breaks the caller.
  debugLog: async (level: 'debug' | 'info' | 'warn' | 'error', message: string, data?: unknown): Promise<void> => {
    try {
      await fetch('/api/debug/log', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ level, message, data }),
        // `keepalive` lets the request survive page unload — handy when the
        // log call sits right before a navigation or a crash.
        keepalive: true,
      });
    } catch {
      // Deliberately ignored.
    }
  },
  whisperStatus: (signal?: AbortSignal) => fetchJSON<{ available: boolean }>('/api/whisper/status', signal),
  transcribe: async (audio: Blob): Promise<string> => {
    // Pick a filename extension the backend can use to identify the format
    const extMap: Record<string, string> = {
      'audio/webm': '.webm',
      'audio/webm;codecs=opus': '.webm',
      'audio/ogg': '.ogg',
      'audio/ogg;codecs=opus': '.ogg',
      'audio/mp4': '.m4a',
      'audio/wav': '.wav',
      'audio/x-wav': '.wav',
    };
    const ext = extMap[audio.type] || '.webm';
    const form = new FormData();
    form.append('audio', audio, `recording${ext}`);
    const resp = await fetch('/api/transcribe', { method: 'POST', body: form });
    if (!resp.ok) throw new Error(await resp.text());
    const data = await resp.json() as { text: string };
    return data.text;
  },

  async systemStats(signal?: AbortSignal): Promise<SystemStats> {
    return fetchJSON<SystemStats>('/api/system/stats', signal);
  },

  /**
   * Auth endpoints — see internal/server/auth.go.
   *
   * `authMe` reports the global auth state; the frontend calls it
   * once at boot to decide whether to render the lockscreen.
   * `authLogin` posts a password and sets the cookie on success;
   * `authLogout` clears it. Neither endpoint itself returns 401 on
   * anonymous access (that's the whole point of /me), so they
   * intentionally don't participate in the AuthError fan-out.
   */
  authMe: () => fetchJSON<AuthMe>('/api/auth/me'),
  authLogin: (password: string) =>
    postJSON<{ ok: boolean }, { password: string }>('/api/auth/login', { password }),
  authLogout: () => postJSON<void, Record<string, never>>('/api/auth/logout', {}, { parseJSON: false }),

  approvedPermissions: (sessionId: string) =>
    fetchJSON<Array<{
      permissionId: string;
      permission: string;
      patterns: string[];
      reasoning: string;
      approvedAt: number;
    }>>(`/api/session/${encodeURIComponent(sessionId)}/approved-permissions`),

  getAutoApprove: (sessionId: string) =>
    fetchJSON<{ enabled: boolean; overridden: boolean }>(
      `/api/session/${encodeURIComponent(sessionId)}/auto-approve`,
    ),

  setAutoApprove: (sessionId: string, enabled: boolean): Promise<void> =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/auto-approve`,
      { enabled },
      { parseJSON: false },
    ),

  getPromptSections: () =>
    fetchJSON<Array<{ title: string; content: string }>>('/api/settings/prompt-sections'),

  setPromptSections: (sections: Array<{ title: string; content: string }>): Promise<void> =>
    postJSON<void>('/api/settings/prompt-sections', sections, { parseJSON: false }),

  getJudgeDelay: () =>
    fetchJSON<{ delayMs: number }>('/api/settings/judge-delay').then((r) => r.delayMs),

  setJudgeDelay: (delayMs: number): Promise<void> =>
    postJSON<void>('/api/settings/judge-delay', { delayMs }, { parseJSON: false }),
};
