import { record as recordPerf, templatePath } from './perfRing';

// Re-export every wire type from the dedicated types module so existing
// imports of `'./api'` continue to work unchanged. New code can import
// from `./api.types` directly.
export type {
  NotifyEntry,
  Session,
  SessionStatus,
  GitInfo,
  Message,
  Part,
  FilePart,
  PartData,
  SessionDetail,
  TaskSessionData,
  ChildSessionReference,
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
  DirectoryBrowseEntry,
  DirectoryBrowseResponse,
  DirectorySearchEntry,
  DirectorySearchResponse,
  ActivityDay,
  ModelUsage,
  HourlyData,
  HourlyTokensByModel,
  PlatformCapabilities,
  PlatformCapabilityEntry,
  PermissionRule,
  CapabilitiesResponse,
  WorktreeEntry,
  WorktreeCreateRequest,
  WorktreeCreateResponse,
  WorktreeRemoveRequest,
  SlashCommand,
  SessionModelEntry,
  FavoriteEntry,
  SessionModelsResponse,
  AgentInfo,
  QueuedMessage,
  TmuxClient,
  TmuxSession,
  TermWindow,
  AuthMe,
  SystemStats,
  SessionNotice,
  SessionWarning,
  ShareLink,
  GlobalShareLink,
  SharedConversation,
  SharingSettings,
  RelaySource,
	WorkflowVersion,
	WorkflowDefinition,
	WorkflowValidation,
	WorkflowRun,
	WorkflowRunDetail,
	WorkflowArtifact,
	WorkflowMapItemRun,
	PromptSchedule,
	DaguStatus,
} from './api.types';

// Type imports used by the api object below.
import type {
  AuthMe,
  CapabilitiesResponse,
  ChildSessionReference,
  FavoriteEntry,
  McpConfigStatus,
  McpConfigInstallResult,
  MetricsDashboard,
  DirectoryBrowseResponse,
  DirectorySearchResponse,
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
  WorktreeRemoveRequest,
  WorktreeEntry,
  WorkingTreeDiff,
  AgentInfo,
  QueuedMessage,
  ActivityDay,
  HourlyData,
  HourlyTokensByModel,
  ShareLink,
  GlobalShareLink,
  SharedConversation,
  SharingSettings,
  PermissionRule,
  RemoteStatus,
  RemoteAccessStatus,
  ResolveTargetsResponse,
	WorkflowVersion,
	WorkflowValidation,
	WorkflowRun,
	WorkflowRunDetail,
	WorkflowArtifact,
	PromptSchedule,
	DaguStatus,
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

/**
 * BackendUnavailableError replaces the cryptic browser errors that a
 * dead/unreachable backend produces — Safari's "Load failed" /
 * "The string did not match the expected pattern.", Chrome's
 * "Failed to fetch" — with one clear, user-facing message. Error
 * banners render `err.message` verbatim, so the friendly copy lives
 * in the message itself.
 */
export class BackendUnavailableError extends Error {
  constructor() {
    super('Backend is not responding. Check that ocman is running, then reload the page.');
    this.name = 'BackendUnavailableError';
  }
}

// Internal: classify a fetch/parse failure. A network-level failure
// (fetch rejects with TypeError) or a non-JSON body on an OK response
// (resp.json() rejects with SyntaxError, e.g. a proxy serving an HTML
// error page) both mean the backend is down or unhealthy. Aborts and
// everything else pass through untouched.
function toBackendError(err: unknown): unknown {
  if (err instanceof DOMException) return err; // AbortError etc.
  if (err instanceof TypeError || err instanceof SyntaxError) {
    return new BackendUnavailableError();
  }
  return err;
}

// Internal: fetch that reports network-level failure as
// BackendUnavailableError. All api.ts call sites go through this.
async function apiFetch(input: string, init?: RequestInit): Promise<Response> {
  try {
    return await fetch(input, init);
  } catch (err) {
    throw toBackendError(err);
  }
}

/**
 * raiseAuthError reports an expired ocman session and returns the
 * AuthError for the caller to throw. Use it from the few call sites
 * that cannot go through fetchJSON / postJSON because they need the
 * raw Response (upstreamApi keeps the backend's structured error
 * envelopes to drive its rate-limit / forge-auth banners). Without
 * this fan-out those panes render a generic error on an expired
 * cookie instead of redirecting to the lockscreen.
 */
export function raiseAuthError(message = 'unauthorized'): AuthError {
  const err = new AuthError(message);
  onAuthError(err);
  return err;
}

// Internal: surface a 401 as an AuthError and notify the registered
// handler. Callers that catch this will receive an AuthError; anyone
// who doesn't catch still lets the handler update global auth state.
//
// Structured error envelopes ({"error":{"code","message"}}, as returned
// by resolveOwner's remote_not_connected and handle-worktree's
// requires_fetch) are unwrapped to their message: callers render
// err.message straight into the UI, so a raw JSON blob would leak.
// Anything else keeps the plain-text body verbatim.
async function throwForStatus(resp: Response): Promise<never> {
  const body = await resp.text().catch(() => '');
  if (resp.status === 401) {
    throw raiseAuthError(body || 'unauthorized');
  }
  throw new Error(envelopeMessage(body) ?? body);
}

// envelopeMessage returns the human-readable message of a structured
// error envelope, or undefined when the body isn't one.
function envelopeMessage(body: string): string | undefined {
  try {
    const parsed = JSON.parse(body) as { error?: { message?: unknown } };
    const msg = parsed?.error?.message;
    return typeof msg === 'string' && msg !== '' ? msg : undefined;
  } catch {
    return undefined;
  }
}

export async function fetchJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const startedAt = performance.now();
  let status = 0;
  try {
    const resp = await apiFetch(url, signal ? { signal } : undefined);
    status = resp.status;
    if (!resp.ok) await throwForStatus(resp);
    return await resp.json().catch((err) => {
      throw toBackendError(err);
    });
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
  opts?: { signal?: AbortSignal; parseJSON?: boolean; method?: 'POST' | 'PATCH' | 'PUT' | 'DELETE' },
): Promise<TResp> {
  const method = opts?.method ?? 'POST';
  const startedAt = performance.now();
  let status = 0;
  try {
    const hasBody = body !== undefined;
    const resp = await apiFetch(url, {
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
    return await resp.json().catch((err) => {
      throw toBackendError(err);
    });
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


// Build a `?a=1&b=2` query suffix from a params object. Skips
// undefined/null values and empty strings (so absent filters don't
// appear in the URL), and returns '' when nothing is set.
function queryString(params?: Record<string, string | number | undefined | null>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params ?? {})) {
    if (v == null || v === '') continue;
    q.set(k, String(v));
  }
  const qs = q.toString();
  return qs ? '?' + qs : '';
}

export const api = {
  stats: (signal?: AbortSignal) => fetchJSON<Stats>('/api/stats', signal),
  metrics: (params?: { agent?: string; model?: string; days?: number; limit?: number; offset?: number; sessionLimit?: number; sessionOffset?: number; projectLimit?: number; projectOffset?: number; dir?: string }, signal?: AbortSignal) =>
    fetchJSON<MetricsDashboard>(`/api/metrics${queryString(params)}`, signal),
  projects: (signal?: AbortSignal) => fetchJSON<Project[]>('/api/projects', signal),
  browseDirectories: (dir?: string, signal?: AbortSignal) =>
    fetchJSON<DirectoryBrowseResponse>(`/api/filesystem/directories${queryString({ dir })}`, signal),
  searchDirectories: (root: string | undefined, query: string, limit?: number, signal?: AbortSignal) => {
    const q = new URLSearchParams({ q: query });
    if (root) q.set('root', root);
    if (limit) q.set('limit', String(limit));
    return fetchJSON<DirectorySearchResponse>(`/api/filesystem/directory-search?${q.toString()}`, signal);
  },
  sessions: (params?: { dir?: string; since?: number; limit?: number }, signal?: AbortSignal) =>
    fetchJSON<Session[]>(`/api/sessions${queryString(params)}`, signal),
  sessionsNotify: (params?: { since?: number; limit?: number }, signal?: AbortSignal) =>
    fetchJSON<NotifyEntry[]>(`/api/sessions/notify${queryString(params)}`, signal),
  session: (id: string, limit = 50, offset = 0, signal?: AbortSignal, platform?: string) => {
    const query = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    if (platform) query.set('platform', platform);
    return fetchJSON<SessionDetail>(`/api/session/${id}?${query.toString()}`, signal);
  },
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
  // --- MCP registration in OpenCode's global config ---
  // Whether OpenCode's global config points at ocman's MCP endpoint.
  getMcpConfig: (signal?: AbortSignal) =>
    fetchJSON<McpConfigStatus>('/api/mcp/config', signal),
  // Write the ocman entry into that config (backs the original up
  // first). Localhost-only on the server.
  installMcpConfig: () =>
    postJSON<McpConfigInstallResult>('/api/mcp/config/install', undefined),

  // --- Global sharing settings + list (Settings page) ---
  // Whether public sharing is allowed (master toggle, on by default).
  // Also reports the relay this instance is configured to use, which is
  // set on the command line and therefore read-only here.
  getSharingEnabled: (signal?: AbortSignal) =>
    fetchJSON<SharingSettings>(`/api/settings/sharing`, signal),
  setSharingEnabled: (enabled: boolean) =>
    postJSON<SharingSettings>(`/api/settings/sharing`, { enabled }),
  // Whether worktree sessions inherit the parent's always-allow
  // permissions at split time (#101; on by default).
  getWorktreeInheritPermissions: (signal?: AbortSignal) =>
    fetchJSON<{ enabled: boolean }>(`/api/settings/worktree-inherit-permissions`, signal),
  setWorktreeInheritPermissions: (enabled: boolean) =>
    postJSON<{ enabled: boolean }>(`/api/settings/worktree-inherit-permissions`, { enabled }),
  // Every active share link across all sessions.
  listAllShares: (signal?: AbortSignal) =>
    fetchJSON<GlobalShareLink[]>(`/api/shares`, signal),
  // Fetch a shared conversation by token. UNAUTHENTICATED endpoint:
  // the token is the only credential. Used by the public /share/:token
  // page.
  sharedConversation: (token: string, signal?: AbortSignal) =>
    fetchJSON<SharedConversation>(`/api/share/${encodeURIComponent(token)}`, signal),
  // Aggregated per-file change summary for a session. Returns
  // supported=false (with HTTP 200) when the owning platform doesn't
  // implement aggregation.
  sessionChanges: (id: string, signal?: AbortSignal) =>
    fetchJSON<SessionChanges>(`/api/session/${encodeURIComponent(id)}/changes`, signal),
  // Per-session info snapshot consumed by the right-hand "Session info"
  // panel: context-window usage, configured MCP servers and their
  // status, configured LSP servers and their status. Returns
  // supported=false (HTTP 200) when the owning platform can't produce
  // a meaningful snapshot (e.g. OpenCode without a live port).
  sessionInfo: (id: string, signal?: AbortSignal) =>
    fetchJSON<SessionInfo>(`/api/session/${encodeURIComponent(id)}/info`, signal),
  /**
   * Batch-fetch sub-session data for multiple task sessions. Returns
   * messages + parts per task so the frontend can render an embedded
   * thread preview inside Task tool cards.
   */
  sessionTasks: (sessionId: string, taskIds: string[], signal?: AbortSignal) =>
    fetchJSON<{ tasks: Record<string, TaskSessionData>; children?: ChildSessionReference[] }>(
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
  // List local branches for the repo containing dir, current branch
  // first. Empty for a non-repo directory.
  gitBranches: (dir: string, signal?: AbortSignal) =>
    fetchJSON<{ branches: string[] }>(
      `/api/git/branches?dir=${encodeURIComponent(dir)}`,
      signal,
    ),
  // Switch the working tree in dir to branch. Rejects (409 → thrown
  // Error whose message names the dirty tree) when uncommitted changes
  // would be overwritten.
  gitCheckout: (dir: string, branch: string) =>
    postJSON<{ branch: string }>('/api/git/checkout', { dir, branch }),
  archiveSession: (platform: string, sessionId: string, timeUpdated: number, archived = true) =>
    postJSON<{ ok: boolean }>('/api/session/archive', { platform, sessionId, timeUpdated, archived }),
  // A project is identified by (remoteId, directory): the same absolute
  // path exists on every attached machine, so the owning host has to ride
  // along or the hub archives its own copy instead.
  archiveProject: (directory: string, archived = true, remoteId?: string) =>
    postJSON<{ ok: boolean }>('/api/project/archive', { directory, archived, remoteId }),
  markSessionSeen: (platform: string, sessionId: string, timeUpdated: number) =>
    postJSON<{ ok: boolean }>('/api/session/seen', { platform, sessionId, timeUpdated }),
  pinSession: (platform: string, sessionId: string, pinned: boolean) =>
    postJSON<{ ok: boolean }>('/api/session/pin', { platform, sessionId, pinned }),
  calcCost: (req: { modelID: string; input: number; output: number; cacheRead: number; cacheWrite: number }) =>
    postJSON<{ cost: number; known: boolean }>('/api/cost/calc', req),
  activity: (params?: { days?: number; model?: string; dir?: string }, signal?: AbortSignal) =>
    fetchJSON<ActivityDay[]>(`/api/activity${queryString(params)}`, signal),
  models: (params?: { days?: number; dir?: string }, signal?: AbortSignal) =>
    fetchJSON<ModelUsage[]>(`/api/models${queryString(params)}`, signal),
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
  hourly: (params?: { days?: number; dir?: string }, signal?: AbortSignal) =>
    fetchJSON<HourlyData[]>(`/api/hourly${queryString(params)}`, signal),
  hourlyTokens: (params?: { days?: number; model?: string; dir?: string }, signal?: AbortSignal) =>
    fetchJSON<HourlyTokensByModel[]>(`/api/hourly-tokens${queryString(params)}`, signal),
  capabilities: (signal?: AbortSignal) => fetchJSON<CapabilitiesResponse>('/api/capabilities', signal),

  // --- Multi-remote support ---
  remoteAccess: (signal?: AbortSignal) =>
    fetchJSON<RemoteAccessStatus>('/api/settings/remote-access', signal),
  revealRemoteToken: () =>
    postJSON<{ token: string }>('/api/settings/remote-access/reveal-token', undefined),
  listRemotes: (signal?: AbortSignal) => fetchJSON<RemoteStatus[]>('/api/remotes', signal),
  addRemote: (body: { address: string; token: string; displayName?: string }) =>
    postJSON<RemoteStatus>('/api/remotes', body),
  updateRemote: (
    localId: number,
    body: { address: string; displayName: string; enabled: boolean; token?: string | null },
  ) => postJSON<{ ok: boolean }>(`/api/remotes/${localId}`, body, { method: 'PUT' }),
  removeRemote: (localId: number) =>
    postJSON<{ ok: boolean }>(`/api/remotes/${localId}`, undefined, { method: 'DELETE', parseJSON: false }),
  reconnectRemote: (localId: number) =>
    postJSON<{ ok: boolean }>(`/api/remotes/${localId}/reconnect`, undefined),
  resolveTargets: (dir: string, remoteId?: string) =>
    postJSON<ResolveTargetsResponse>('/api/sessions/resolve-targets', { dir, ...(remoteId ? { remoteId } : {}) }),
  createSession: async (directory: string, platform?: string, title?: string) => {
    const resp = await apiFetch('/api/sessions', {
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
    platform?: string,
    // queue=true holds the message in the follow-up queue for the
    // session's next idle edge instead of sending it now (#58). Set from
    // the user's explicit Ctrl/Cmd+Enter gesture. Without it the server
    // sends immediately, mid-turn included.
    queue?: boolean,
  ) => {
    const query = platform ? `?platform=${encodeURIComponent(platform)}` : '';
    const resp = await apiFetch(`/api/session/${encodeURIComponent(sessionId)}/message${query}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message, images, model, agent, reasoning, queue }),
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
  // --- Follow-up message queue (#58) ---
  // Prompts submitted while a session is mid-turn are queued server-side
  // and drain one per turn on idle. These endpoints let the composer show
  // and manage that shared queue.
  queuedMessages: async (sessionId: string, platform?: string): Promise<QueuedMessage[]> => {
    const query = platform ? `?platform=${encodeURIComponent(platform)}` : '';
    const resp = await apiFetch(`/api/session/${encodeURIComponent(sessionId)}/queue${query}`);
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    return resp.json() as Promise<QueuedMessage[]>;
  },
  deleteQueuedMessage: async (sessionId: string, queuedId: string, platform?: string) => {
    const query = platform ? `?platform=${encodeURIComponent(platform)}` : '';
    const resp = await apiFetch(
      `/api/session/${encodeURIComponent(sessionId)}/queue/${encodeURIComponent(queuedId)}${query}`,
      { method: 'DELETE' },
    );
    if (!resp.ok && resp.status !== 404) throw new Error(`HTTP ${resp.status}`);
  },
  moveQueuedMessage: async (
    sessionId: string,
    queuedId: string,
    direction: -1 | 1,
    platform?: string,
  ) => {
    const query = platform ? `?platform=${encodeURIComponent(platform)}` : '';
    const resp = await apiFetch(
      `/api/session/${encodeURIComponent(sessionId)}/queue/${encodeURIComponent(queuedId)}/move${query}`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ direction }),
      },
    );
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
  },
  uploadComposerAttachment: async (sessionId: string, file: File) => {
    const form = new FormData();
    form.append('file', file);
    const resp = await apiFetch(`/api/session/${encodeURIComponent(sessionId)}/attachment`, {
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
  getPermissionRules: (sessionId: string) =>
    fetchJSON<{ rules: PermissionRule[] }>(
      `/api/session/${encodeURIComponent(sessionId)}/permission-rules`,
    ),
  setPermissionRules: (sessionId: string, rules: PermissionRule[]) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/permission-rules`,
      { rules },
      { method: 'PUT', parseJSON: false },
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
  revertSession: (sessionId: string, messageID: string) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/revert`,
      { messageID },
      { parseJSON: false },
    ),
  unrevertSession: (sessionId: string) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/unrevert`,
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
  tmuxLaunchOpencode: (directory: string, remoteId?: string): Promise<{ session: string }> =>
    postJSON<{ session: string }>('/api/tmux/launch-opencode', { directory, ...(remoteId ? { remoteId } : {}) }),
  /**
   * In-app terminal windows. Each entry is a dedicated tmux window
   * (`ocman-term-<slug>-<n>`) backing a terminal tab in the UI.
   */
  term: {
    listWindows: (dir: string, remoteId?: string, signal?: AbortSignal) => {
      const params = new URLSearchParams({ dir });
      if (remoteId && remoteId !== 'local') params.set('remoteId', remoteId);
      return fetchJSON<{ windows: TermWindow[] }>(
        `/api/term/windows?${params.toString()}`,
        signal,
      );
    },
    createWindow: async (dir: string, remoteId?: string): Promise<{ window: string }> => {
      const resp = await apiFetch('/api/term/windows', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dir, ...(remoteId && remoteId !== 'local' ? { remoteId } : {}) }),
      });
      if (!resp.ok) throw new Error(await resp.text());
      return resp.json();
    },
    killWindow: async (dir: string, window: string, remoteId?: string): Promise<void> => {
      const resp = await apiFetch('/api/term/windows', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dir, window, ...(remoteId && remoteId !== 'local' ? { remoteId } : {}) }),
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
    remove: (req: WorktreeRemoveRequest): Promise<{ removed: boolean }> =>
      postJSON<{ removed: boolean }>('/api/worktree/remove', req),
  },
	promptSchedules: {
		list: (directory: string, remoteId = 'local', signal?: AbortSignal) => fetchJSON<PromptSchedule[]>(`/api/prompt-schedules?directory=${encodeURIComponent(directory)}&remoteId=${encodeURIComponent(remoteId)}`, signal),
		get: (id: string, signal?: AbortSignal) => fetchJSON<PromptSchedule>(`/api/prompt-schedules/${encodeURIComponent(id)}`, signal),
		create: (req: { directory: string; remoteId: string; prompt: string; runAt?: number; timingType: 'once' | 'interval' | 'cron'; intervalMinutes?: number; cron?: string; timezone: string; sessionMode: 'fresh' | 'reuse' }) => postJSON<PromptSchedule>('/api/prompt-schedules', req),
		cancel: (id: string) => postJSON<PromptSchedule>(`/api/prompt-schedules/${encodeURIComponent(id)}/cancel`, {}),
		runNow: (id: string) => postJSON<PromptSchedule>(`/api/prompt-schedules/${encodeURIComponent(id)}/run-now`, {}),
		setEnabled: (id: string, enabled: boolean) => postJSON<PromptSchedule>(`/api/prompt-schedules/${encodeURIComponent(id)}/${enabled ? 'enable' : 'disable'}`, {}),
	},
	dagu: {
		status: (remoteId = 'local', signal?: AbortSignal) => fetchJSON<DaguStatus>(`/api/dagu/status?remoteId=${encodeURIComponent(remoteId)}`, signal),
	},
	workflows: {
		versions: (signal?: AbortSignal) => fetchJSON<WorkflowVersion[]>('/api/workflows', signal),
		validate: (source: string) => postWorkflowSource<WorkflowValidation>('/api/workflows/validate', source),
		publish: async (source: string): Promise<WorkflowVersion> => {
			return postWorkflowSource<WorkflowVersion>('/api/workflows', source);
		},
		activate: (versionId: string) => postJSON<WorkflowVersion>(`/api/workflows/${encodeURIComponent(versionId)}/activate`, {}),
		deactivate: (versionId: string) => postJSON<WorkflowVersion>(`/api/workflows/${encodeURIComponent(versionId)}/deactivate`, {}),
		archive: (versionId: string) => postJSON<void>(`/api/workflows/${encodeURIComponent(versionId)}`, undefined, { method: 'DELETE' }),
		startActive: (workflowId: string) => postJSON<WorkflowRunDetail>(`/api/workflows/${encodeURIComponent(workflowId)}/start`, {}),
		start: (versionId: string) => postJSON<WorkflowRunDetail>(`/api/workflows/${encodeURIComponent(versionId)}/runs`, {}),
		runs: (signal?: AbortSignal) => fetchJSON<WorkflowRun[]>('/api/workflow-runs', signal),
		run: (runId: string, signal?: AbortSignal) => fetchJSON<WorkflowRunDetail>(`/api/workflow-runs/${encodeURIComponent(runId)}`, signal),
		approve: (runId: string, nodeId: string) => postJSON<WorkflowRunDetail>(`/api/workflow-runs/${encodeURIComponent(runId)}/approve/${encodeURIComponent(nodeId)}`, {}),
		pause: (runId: string) => postJSON<WorkflowRunDetail>(`/api/workflow-runs/${encodeURIComponent(runId)}/pause`, {}),
		cancel: (runId: string) => postJSON<WorkflowRunDetail>(`/api/workflow-runs/${encodeURIComponent(runId)}/cancel`, {}),
		resolveUnknown: (runId: string, attemptId: number, resolution: 'successful' | 'failed' | 'retry') => postJSON<WorkflowRunDetail>(`/api/workflow-runs/${encodeURIComponent(runId)}/resolve-unknown/${attemptId}`, { resolution }),
		retryFrom: (runId: string, nodeId: string, versionId?: string) => postJSON<WorkflowRunDetail>(`/api/workflow-runs/${encodeURIComponent(runId)}/retry-from/${encodeURIComponent(nodeId)}`, { versionId: versionId ?? '' }),
		artifacts: (runId: string, signal?: AbortSignal) => fetchJSON<WorkflowArtifact[]>(`/api/workflow-runs/${encodeURIComponent(runId)}/artifacts`, signal),
		artifactDownloadUrl: (runId: string, artifactId: string) => `/api/workflow-runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(artifactId)}/download`,
	},
  compactSession: (sessionId: string, providerID: string, modelID: string) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/compact`,
      { providerID, modelID },
      { parseJSON: false },
    ),
  // Fork a session into a new child session, optionally from a specific
  // message. Returns the new session's ID.
  forkSession: (sessionId: string, messageID?: string) =>
    postJSON<{ id: string }>(
      `/api/session/${encodeURIComponent(sessionId)}/fork`,
      { messageID: messageID ?? '' },
    ),
  // Move a session to another project directory on the same host.
  moveSession: (sessionId: string, directory: string) =>
    postJSON<void>(
      `/api/session/${encodeURIComponent(sessionId)}/move`,
      { directory },
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
  restartOpencode: (sessionId: string): Promise<{ target: string }> =>
    postJSON<{ target: string }>(`/api/session/${encodeURIComponent(sessionId)}/restart-opencode`, undefined),
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
    const resp = await apiFetch('/api/transcribe', { method: 'POST', body: form });
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
    fetchJSON<Array<{ title: string; content: string; enabled?: boolean }>>(
      '/api/settings/prompt-sections',
    ),

  setPromptSections: (
    sections: Array<{ title: string; content: string; enabled?: boolean }>,
  ): Promise<void> =>
    postJSON<void>('/api/settings/prompt-sections', sections, { parseJSON: false }),

  getJudgeDelay: () =>
    fetchJSON<{ delayMs: number }>('/api/settings/judge-delay').then((r) => r.delayMs),

  setJudgeDelay: (delayMs: number): Promise<void> =>
    postJSON<void>('/api/settings/judge-delay', { delayMs }, { parseJSON: false }),

  getJudgeModel: () =>
    fetchJSON<{ model: string }>('/api/settings/judge-model').then((r) => r.model),

  setJudgeModel: (model: string): Promise<void> =>
    postJSON<void>('/api/settings/judge-model', { model }, { parseJSON: false }),
};

async function postWorkflowSource<T>(url: string, source: string): Promise<T> {
	const response = await apiFetch(url, { method: 'POST', headers: { 'Content-Type': 'application/yaml' }, body: source });
	if (!response.ok) throw new Error(await response.text());
	return response.json();
}
