import { record as recordPerf, templatePath } from './perfRing';

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
 * postJSON is the POST counterpart to fetchJSON: JSON body in, JSON
 * body out, with identical 401 handling. Use it for new call sites;
 * existing inline `fetch(..., { method: 'POST' })` usages in this
 * module will migrate opportunistically.
 *
 * Set `parseJSON` to false when the server returns 204 No Content
 * (login returns a body, but logout doesn't).
 */
export async function postJSON<TResp, TReq = unknown>(
  url: string,
  body: TReq,
  opts?: { signal?: AbortSignal; parseJSON?: boolean },
): Promise<TResp> {
  const startedAt = performance.now();
  let status = 0;
  try {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
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
      method: 'POST',
      status,
      durationMs: performance.now() - startedAt,
      startedAt,
    });
  }
}

/**
 * Minimal per-session projection returned by /api/sessions/notify.
 * Only sessions that could drive the favicon/title notification state
 * (or the in-app prompt toast) are included in the response, so the
 * caller can simply check whether the array is non-empty to decide
 * whether to show a badge.
 *
 * `title` and `directory` are present on every row; they're optional
 * here only because the JSON payload omits empty strings via
 * `omitempty` on the server side.
 */
export interface NotifyEntry {
  id: string;
  status: string;
  seen: boolean;
  pendingPermission?: boolean;
  pendingQuestion?: boolean;
  title?: string;
  directory?: string;
}

export interface Session {
  id: string;
  /**
   * Stable identifier of the coding-agent platform that owns this
   * session (e.g. 'opencode', 'claude-code'). Populated by the backend.
   *
   * The frontend must not branch on this value — use the capabilities
   * endpoint for feature gating instead.
   *
   * Terminology: this is the *platform* (the tool that produced the
   * session), not the composer-level *agent* role ("build", "plan",
   * subagent, ...) that OpenCode surfaces in MessageData.agent.
   */
  platform: string;
  projectId: string;
  title: string;
  directory: string;
  timeCreated: number;
  timeUpdated: number;
  summaryAdditions: number | null;
  summaryDeletions: number | null;
  summaryFiles: number | null;
  shareUrl: string | null;
  messageCount: number;
  durationMs: number;
  totalInputTokens: number;
  totalOutputTokens: number;
  totalCost: number;
  status: 'waiting' | 'busy' | 'done' | 'error';
  /**
   * True when the owning adapter has a live channel to this session's
   * running agent process. For OpenCode this means a --port was
   * discovered for the session's directory; for Claude Code (future)
   * it means the session's jsonl is held open or a hook fired recently.
   */
  liveConnection: boolean;
  pendingPermission: boolean;
  pendingQuestion: boolean;
  archived: boolean;
  seen: boolean;
}

/**
 * Mirror of internal/db.GitInfo. Returned per directory by
 * /api/git/info; consumed by useGitInfo and rendered via
 * GitStatusLine. No longer attached to Session payloads — fetching
 * it eagerly forced the backend to fork `git status` on every
 * dashboard poll, producing multi-second pauses (see
 * docs/profiling.md).
 */
export interface GitInfo {
  branch: string;
  ahead: number;
  behind: number;
  dirty: boolean;
}

export interface Message {
  id: string;
  sessionId: string;
  timeCreated: number;
  data: {
    role: string;
    finish?: string;
    modelID?: string;
    providerID?: string;
    agent?: string;
    mode?: string;
    cost?: number;
    tokens?: { input: number; output: number; reasoning?: number; cache?: { read?: number; write?: number } };
    time?: { created: number; completed?: number };
    error?: {
      name?: string;
      data?: {
        message?: string;
        statusCode?: number;
      };
    };
  };
}

export interface Part {
  id: string;
  messageId: string;
  sessionId: string;
  data: string | PartData;
}

export interface FilePart {
  type: 'file';
  mime: string;
  url: string;
  filename?: string;
}

export interface PartData {
  type: string;
  text?: string;
  tool?: string;
  // File part fields (for type === 'file')
  mime?: string;
  url?: string;
  filename?: string;
  state?: {
    status?: string;
    input?: Record<string, unknown>;
    output?: unknown;
    title?: string;
    metadata?: {
      description?: string;
      // Edit/Write tools include a filediff with the full file before/after
      // the change. We use this to render a diff with surrounding context
      // beyond what oldString/newString alone would show.
      filediff?: {
        file?: string;
        before?: string;
        after?: string;
        additions?: number;
        deletions?: number;
      };
    };
    attachments?: FilePart[];
  };
  file?: string;
  path?: string;
  content?: string;
  diff?: string;
}

export interface SessionDetail {
  session: Session & { contextTokenCount?: number };
  messages: Message[];
  parts: Part[];
  totalMessages?: number;
  contextTokenCount?: number;
  defaultAgent?: string;
  defaultModel?: string;
}

// One file-touching tool call inside a session. Returned as part of
// SessionChanges; the sidebar expands these inside the per-file
// "individual edits" disclosure. `patch` is the unified-diff body
// when OpenCode supplies one (modern schema); `before`/`after` are
// the legacy snapshot pair preserved for older parts. Consumers
// prefer `patch` and fall back to a Before/After diff.
export interface SessionEdit {
  partId: string;
  messageId: string;
  timeCreated: number;
  tool: string;
  additions: number;
  deletions: number;
  patch?: string;
  before?: string;
  after?: string;
}

// One file's roll-up across the entire session. `patch` is the
// concatenation of every edit's unified-diff body in chronological
// order — it's what the sidebar renders by default. `before` and
// `after` are the legacy snapshot pair (first-edit-before /
// last-edit-after) preserved for older OpenCode versions; the
// renderer falls back to a Before/After diff when `patch` is empty.
// `additions`/`deletions` are summed per-edit and authoritative.
export interface FileChange {
  path: string;
  displayPath: string;
  additions: number;
  deletions: number;
  editCount: number;
  firstEditAt: number;
  lastEditAt: number;
  patch?: string;
  before?: string;
  after?: string;
  edits: SessionEdit[];
}

// One file in a working-tree git diff. Mirrors gitinfo.DiffFile.
// `diff` is a unified-diff body (the full `diff --git a/... b/...`
// section for the file) and is empty for binary files.
export interface WorkingTreeFile {
  path: string;
  status: 'modified' | 'added' | 'deleted' | 'renamed' | 'untracked';
  oldPath?: string;
  additions: number;
  deletions: number;
  diff: string;
  isBinary: boolean;
}

// Wire shape for /api/git/diff. Mirrors gitinfo.Diff.
export interface WorkingTreeDiff {
  repo: string;
  branch: string;
  ahead: number;
  behind: number;
  files: WorkingTreeFile[];
  truncated: boolean;
}

// Wire shape for /api/session/{id}/changes. supported=false is
// returned (with HTTP 200) for adapters that don't aggregate file
// changes (Claude Code today). The frontend renders a "Not supported
// on this platform" empty state in that case.
export interface SessionChanges {
  sessionId: string;
  supported: boolean;
  totalAdditions: number;
  totalDeletions: number;
  filesChanged: number;
  files: FileChange[];
}

// One configured MCP (Model Context Protocol) server plus its current
// connection status, as exposed by the running platform's MCP catalog.
// Status uses the upstream platform's vocabulary verbatim ("connected"
// / "needs_auth" / "failed" / future values) — the renderer styles known
// values and falls back to neutral styling for the rest.
export interface MCPServer {
  name: string;
  status: string;
  error?: string;
}

// One configured LSP plus its current status. `id` is the platform-
// supplied stable identifier; `name` is the human-friendly label
// (often equal to id). `status` follows the same verbatim rule as
// MCPServer.status.
export interface LSPServer {
  id: string;
  name: string;
  status: string;
}

// Context-window usage snapshot for a session. `tokens` is the
// running count attributed to the most recent assistant turn (same
// value as SessionDetail.contextTokenCount). `limit` is the model's
// context-window size in tokens — 0 / absent when unknown (catalog
// unreachable or model not in catalog). `cost` is total session
// spend in USD.
export interface SessionInfoContext {
  tokens: number;
  limit?: number;
  /**
   * Sum of the platform-recorded `cost` field across assistant
   * messages — what was actually billed. Zero for subscription-plan
   * accounts whose messages all record cost=0.
   */
  cost: number;
  /**
   * Token-derived cost estimate from the loaded pricing table,
   * computed independently of `cost`. Surfaced as a separate "Est"
   * row in the panel so subscription-plan sessions ($0 Cost +
   * non-zero Est) are visible at a glance.
   */
  estCost: number;
  model?: string;
}

// Lifetime token totals broken down across the four buckets the
// SessionInfo panel surfaces. Cache read/write are not in the
// /api/sessions wire payload; this is the only place a client can
// reliably read them.
export interface SessionInfoTokens {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}

// One row of an OpenCode / Claude Code todowrite call.
export interface SessionInfoTodo {
  content: string;
  status: string;
  priority?: string;
}

// Wire shape for /api/session/{id}/info.
//
// `supported` reflects the *live* tier specifically: when false, MCP
// servers, LSP servers, and the per-model context-window limit are
// not available because the platform doesn't have a live channel
// (e.g. OpenCode without a discoverable --port). Lifetime token
// totals and the latest todo list are computed from the read-only DB
// and are populated either way.
// User / assistant turn breakdown for the session. Both zero when
// the platform doesn't compute the breakdown — the UI then falls back
// to the legacy `session.messageCount` (user turns only).
export interface SessionInfoMessages {
  user: number;
  assistant: number;
}

export interface SessionInfo {
  sessionId: string;
  supported: boolean;
  context: SessionInfoContext;
  tokens: SessionInfoTokens;
  mcpServers: MCPServer[];
  lspServers: LSPServer[];
  messages: SessionInfoMessages;
  todos?: SessionInfoTodo[];
}

export interface Stats {
  totalSessions: number;
  totalMessages: number;
  totalProjects: number;
  totalTokensIn: number;
  totalTokensOut: number;
  totalCost: number;
}

export interface MetricsSummary {
  requests: number;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  avgTokensPerSec: number;
  avgDurationMs: number;
  totalDurationMs: number;
  cacheHitRate: number;
  totalCost: number;
  totalCalcCost: number;
}

export interface MetricsPoint {
  label: string;
  avgOutputTokensSec: number;
  cumulativeCost: number;
  inputTokens: number;
  cacheReadTokens: number;
  outputTokens: number;
  avgDurationMs: number;
  avgCacheEfficiency: number;
  count: number;
}

export interface StopReasonCount {
  reason: string;
  count: number;
}

export interface RequestMetricsRow {
  id: string;
  sessionId: string;
  timeCreated: number;
  agent: string;
  model: string;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  tokensPerSecond: number;
  durationMs: number;
  cost: number;
  calcCost: number;
  stopReason: string;
}

export interface SessionLogEntry {
  id: string;
  title: string;
  directory: string;
  firstRequestTime: number;
  lastRequestTime: number;
  requests: number;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  totalDurationMs: number;
  avgTokensPerSec: number;
  cost: number;
  calcCost: number;
  agents: string[];
  models: string[];
  errorCount: number;
}

export interface ProjectLogEntry {
  directory: string;
  sessions: number;
  requests: number;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  totalTokens: number;
  totalDurationMs: number;
  avgTokensPerSec: number;
  cost: number;
  calcCost: number;
  models: string[];
  errorCount: number;
  lastRequestTime: number;
}

export interface MetricsDashboard {
  availableAgents: string[];
  availableModels: string[];
  summary: MetricsSummary;
  series: MetricsPoint[];
  stopReasons: StopReasonCount[];
  requests: RequestMetricsRow[];
  totalRequests: number;
  sessions: SessionLogEntry[];
  totalSessions: number;
  projects: ProjectLogEntry[];
  totalProjects: number;
}

export interface Project {
  directory: string;
  sessionCount: number;
  messageCount: number;
  totalTokensIn: number;
  totalTokensOut: number;
  lastUsed: number;
}

export interface ActivityDay {
  date: string;
  sessions: number;
  messages: number;
}

export interface ModelUsage {
  provider: string;
  model: string;
  count: number;
  tokensIn: number;
  tokensOut: number;
}

export interface HourlyData {
  hour: number;
  sessions: number;
}

export interface HourlyTokensByModel {
  datetime: string; // "YYYY-MM-DD HH"
  provider: string;
  model: string;
  tokensIn: number;
  tokensOut: number;
}

/**
 * Capability flags reported by a platform adapter. Mirrors
 * internal/platforms.Capabilities. The frontend gates UI affordances
 * on these flags rather than on platform identity comparisons.
 */
export interface PlatformCapabilities {
  composer: boolean;
  respondPermission: boolean;
  respondQuestion: boolean;
  abort: boolean;
  compact: boolean;
  events: boolean;
  agentCatalog: boolean;
  modelCatalog: boolean;
  slashCommands: boolean;
  /**
   * Whether the platform exposes /api/session/{id}/changes — the
   * per-file change aggregation used by the session-changes sidebar.
   * False for adapters that can't compute a useful summary
   * (Claude Code today).
   */
  fileChanges: boolean;
  /**
   * Whether the platform exposes /api/session/{id}/info — the
   * per-session context-window / MCP / LSP snapshot used by the
   * "Session info" right-hand panel. False for adapters that can't
   * produce a useful snapshot (Claude Code today).
   */
  sessionInfo: boolean;
  /**
   * Short, user-facing message explaining how to establish the live
   * connection to a running agent instance when it's missing (e.g.
   * "Start OpenCode with `opencode --port 0` ..." for OpenCode).
   * Empty / absent when the platform has no such setup step.
   */
  liveConnectionHint?: string;
}

export interface PlatformCapabilityEntry {
  id: string;
  displayName: string;
  available: boolean;
  capabilities: PlatformCapabilities;
}

export interface CapabilitiesResponse {
  platforms: PlatformCapabilityEntry[];
}

export interface SlashCommand {
  name: string;
  description?: string;
  agent?: string;
  model?: string;
}

// SessionModelEntry mirrors internal/server/handlers.go:sessionModelEntry —
// one row of the model palette built by GET /api/session-models/{id}.
//
// Ordering signals are computed server-side; the client just renders:
// - recentRank: 1-based position in the "recently used" list (0 = not recent)
// - isSessionDefault: last model used in this session (strongest signal)
// - isProviderDefault: OpenCode's default for this provider
// - isAvailable: provider is in /provider's `connected` set (user has it set up)
export interface SessionModelEntry {
  provider: string;
  providerName?: string;
  model: string;
  modelName?: string;
  recentRank?: number;
  isSessionDefault?: boolean;
  isProviderDefault?: boolean;
  isAvailable?: boolean;
  isFavorite?: boolean;
  reasoning?: string[];
}

// FavoriteEntry mirrors internal/server/favorites.go:favoriteEntry.
// Favorites are scoped per-platform in state.db so the same model id
// can be starred independently across OpenCode and Claude Code.
export interface FavoriteEntry {
  platform: string;
  provider: string;
  model: string;
}

export interface SessionModelsResponse {
  sessionDefault?: string;
  providerDefaults?: Record<string, string>;
  hasProviders: boolean;
  models: SessionModelEntry[];
}

export interface AgentInfo {
  name: string;
  description?: string;
  mode?: 'primary' | 'subagent' | 'all';
  model?: string | { providerID?: string; modelID?: string };
  color?: string;
  hidden?: boolean;
  builtIn?: boolean;
}

export interface TmuxClient {
  tty: string;
  session: string;
  width: string;
  height: string;
}

export interface TmuxSession {
  name: string;
  resolvedPath: string;
  windows: number;
}

export const api = {
  stats: () => fetchJSON<Stats>('/api/stats'),
  metrics: (params?: { agent?: string; model?: string; days?: number; limit?: number; offset?: number; sessionLimit?: number; sessionOffset?: number; projectLimit?: number; projectOffset?: number }) => {
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
    const qs = q.toString();
    return fetchJSON<MetricsDashboard>(`/api/metrics${qs ? '?' + qs : ''}`);
  },
  projects: () => fetchJSON<Project[]>('/api/projects'),
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
  // Working-tree git diff for an absolute directory. fresh=1 bypasses
  // the backend's tiny in-process cache; the SSE-driven refetch path
  // sets it so an edit-event-triggered refresh is never stale.
  gitDiff: (dir: string, opts?: { fresh?: boolean }, signal?: AbortSignal) => {
    const q = new URLSearchParams({ dir });
    if (opts?.fresh) q.set('fresh', '1');
    return fetchJSON<WorkingTreeDiff>(`/api/git/diff?${q.toString()}`, signal);
  },
  archiveSession: async (platform: string, sessionId: string, timeUpdated: number, archived = true) => {
    const resp = await fetch('/api/session/archive', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ platform, sessionId, timeUpdated, archived }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    return resp.json() as Promise<{ ok: boolean }>;
  },
  markSessionSeen: async (platform: string, sessionId: string, timeUpdated: number) => {
    const resp = await fetch('/api/session/seen', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ platform, sessionId, timeUpdated }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    return resp.json() as Promise<{ ok: boolean }>;
  },
  calcCost: async (req: { modelID: string; input: number; output: number; cacheRead: number; cacheWrite: number }) => {
    const resp = await fetch('/api/cost/calc', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    });
    if (!resp.ok) throw new Error(await resp.text());
    return resp.json() as Promise<{ cost: number; known: boolean }>;
  },
  activity: (params?: { days?: number; model?: string }) => {
    const q = new URLSearchParams();
    if (params?.days) q.set('days', String(params.days));
    if (params?.model) q.set('model', params.model);
    const qs = q.toString();
    return fetchJSON<ActivityDay[]>(`/api/activity${qs ? '?' + qs : ''}`);
  },
  models: (params?: { days?: number }) => {
    const q = new URLSearchParams();
    if (params?.days) q.set('days', String(params.days));
    const qs = q.toString();
    return fetchJSON<ModelUsage[]>(`/api/models${qs ? '?' + qs : ''}`);
  },
  sessionModels: (sessionId: string) =>
    fetchJSON<SessionModelsResponse>(`/api/session/${encodeURIComponent(sessionId)}/models`),
  // Favorites CRUD. Scoped per-platform because the same (provider,
  // model) pair can legitimately be a favorite under one platform but
  // not another — matches the DB's composite key.
  listFavorites: (platform: string) =>
    fetchJSON<FavoriteEntry[]>(`/api/favorites?platform=${encodeURIComponent(platform)}`),
  addFavorite: async (platform: string, provider: string, model: string) => {
    const resp = await fetch('/api/favorites', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ platform, provider, model }),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  removeFavorite: async (platform: string, provider: string, model: string) => {
    const resp = await fetch('/api/favorites', {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ platform, provider, model }),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  hourly: (params?: { days?: number }) => {
    const q = new URLSearchParams();
    if (params?.days) q.set('days', String(params.days));
    const qs = q.toString();
    return fetchJSON<HourlyData[]>(`/api/hourly${qs ? '?' + qs : ''}`);
  },
  hourlyTokens: (params?: { days?: number; model?: string }) => {
    const q = new URLSearchParams();
    if (params?.days) q.set('days', String(params.days));
    if (params?.model) q.set('model', params.model);
    const qs = q.toString();
    return fetchJSON<HourlyTokensByModel[]>(`/api/hourly-tokens${qs ? '?' + qs : ''}`);
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
      throw new Error(body || `HTTP ${resp.status}`);
    }
  },
  listPermissions: (sessionId: string) =>
    fetchJSON<unknown[]>(`/api/session/${encodeURIComponent(sessionId)}/permissions`),
  respondPermission: async (
    sessionId: string,
    permissionId: string,
    reply: 'once' | 'always' | 'reject',
  ) => {
    const resp = await fetch(
      `/api/session/${encodeURIComponent(sessionId)}/permissions/${encodeURIComponent(permissionId)}`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reply }),
      },
    );
    if (!resp.ok) throw new Error(await resp.text());
  },
  listQuestions: (sessionId: string) =>
    fetchJSON<unknown[]>(`/api/session/${encodeURIComponent(sessionId)}/questions`),
  respondQuestion: async (
    sessionId: string,
    requestId: string,
    answers: string[][],
  ) => {
    const resp = await fetch(
      `/api/session/${encodeURIComponent(sessionId)}/questions/${encodeURIComponent(requestId)}`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ answers }),
      },
    );
    if (!resp.ok) throw new Error(await resp.text());
  },
  rejectQuestion: async (sessionId: string, requestId: string) => {
    const resp = await fetch(
      `/api/session/${encodeURIComponent(sessionId)}/questions/${encodeURIComponent(requestId)}/reject`,
      { method: 'POST' },
    );
    if (!resp.ok) throw new Error(await resp.text());
  },
  abortSession: async (sessionId: string) => {
    const resp = await fetch(`/api/session/${encodeURIComponent(sessionId)}/abort`, {
      method: 'POST',
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  tmuxClients: () => fetchJSON<{ available: boolean; clients: TmuxClient[] }>('/api/tmux/clients'),
  tmuxSessions: () => fetchJSON<{ available: boolean; sessions: TmuxSession[] }>('/api/tmux/sessions'),
  tmuxSwitch: async (session: string, client?: string) => {
    const body: Record<string, string> = { session };
    if (client) body.client = client;
    const resp = await fetch('/api/tmux/switch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  tmuxLaunchOpencode: async (directory: string): Promise<{ session: string }> => {
    const resp = await fetch('/api/tmux/launch-opencode', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ directory }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    return resp.json();
  },
  compactSession: async (sessionId: string, providerID: string, modelID: string) => {
    const resp = await fetch(`/api/session/${encodeURIComponent(sessionId)}/compact`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ providerID, modelID }),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  commands: (sessionId: string, signal?: AbortSignal) =>
    fetchJSON<SlashCommand[]>(`/api/session/${encodeURIComponent(sessionId)}/commands`, signal),
  agents: (sessionId: string, signal?: AbortSignal) =>
    fetchJSON<AgentInfo[]>(`/api/session/${encodeURIComponent(sessionId)}/agents`, signal),
  executeCommand: async (
    sessionId: string,
    command: string,
    args: string,
    model?: string,
    agent?: string,
  ) => {
    const resp = await fetch(`/api/session/${encodeURIComponent(sessionId)}/command`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ command, arguments: args, model, agent }),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  renameSession: async (sessionId: string, title: string) => {
    const resp = await fetch(`/api/session/${encodeURIComponent(sessionId)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title }),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
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
  whisperStatus: () => fetchJSON<{ available: boolean }>('/api/whisper/status'),
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

  async systemStats(): Promise<SystemStats> {
    return fetchJSON<SystemStats>('/api/system/stats');
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
};

export interface AuthMe {
  authRequired: boolean;
  authenticated: boolean;
}

export interface SystemStats {
  memory: {
    alloc: number;
    totalAlloc: number;
    sys: number;
    heapAlloc: number;
    heapSys: number;
    heapInuse: number;
    heapIdle: number;
    heapReleased: number;
  };
  gc: {
    numGC: number;
    lastGC: number;
    pauseNs: number;
  };
  goroutines: number;
  uptime: number;
}
