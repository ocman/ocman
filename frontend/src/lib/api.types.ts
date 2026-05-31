/**
 * Type-only mirror of the wire shapes exposed by ocman's HTTP API.
 *
 * This file holds the data definitions that used to live inside
 * `api.ts` itself. Splitting them into a dedicated module lets
 * consumers import types without dragging in the network helpers
 * (and the perf-ring instrumentation they pull in transitively).
 *
 * `api.ts` re-exports every name from this module, so existing
 * imports of `'./api'` continue to work unchanged.
 */

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
	 * session (e.g. 'opencode'). Populated by the backend.
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
  /**
   * Time the agent was actually working on a turn — the sum of
   * (time.completed - time.created) across assistant messages.
   * Excludes the idle gap between an assistant's `completed` and the
   * next user message (user think time, permission prompts answered
   * between turns).
   *
   * 0 when the platform doesn't expose per-turn timestamps, or when
   * the session payload was served from the listing endpoint (which
   * doesn't scan messages). The session-detail endpoint populates it.
   */
  activeDurationMs: number;
  totalInputTokens: number;
  totalOutputTokens: number;
  totalCost: number;
  status: 'waiting' | 'busy' | 'done' | 'error';
	/**
	 * True when the owning adapter has a live channel to this session's
	 * running agent process. For OpenCode this means a --port was
	 * discovered for the session's directory.
	 */
  liveConnection: boolean;
  pendingPermission: boolean;
  pendingQuestion: boolean;
  archived: boolean;
  seen: boolean;
  pinned: boolean;
  pinnedAt: number;
  /**
   * Normalized, platform-agnostic explanation of a transient session
   * condition (e.g. rate-limit backoff). Absent when no notice applies.
   * The frontend renders this as a banner / tooltip without inspecting
   * the platform field.
   */
  notice?: SessionNotice;
}

/**
 * A normalized session notice surfaced by the backend when the latest
 * assistant error matches a known transient pattern. Currently the only
 * kind is `"rate_limit"`.
 */
export interface SessionNotice {
  kind: 'rate_limit' | string;
  /** User-facing summary of the condition. */
  message: string;
  /** Unix ms timestamp when retry is expected, or 0 when unknown. */
  retryAt: number;
  /** Retry attempt number, or 0 when unknown. */
  attempt: number;
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
  /** Unix ms timestamp when the part was created. Present in DB
   *  responses; absent for SSE-constructed parts during streaming. */
  timeCreated?: number;
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
  /** Present when type === 'auto-approved' (synthetic notice part). */
  permission?: string;
  patterns?: string[];
  /** Judge's one-line conclusion shown inline on the auto-approved
   *  notice. Optional — may be empty for legacy approvals or when the
   *  model omitted the field. */
  reasoning?: string;
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
      ocmanUserExecutedShell?: boolean;
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

/**
 * Per-task sub-session data returned by /api/session/{id}/tasks.
 * Contains the messages and parts needed to render an embedded
 * thread preview inside a Task tool card.
 */
export interface TaskSessionData {
  messages: Message[];
  parts: Part[];
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
// changes. The frontend renders a "Not supported on this platform"
// empty state in that case.
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

// One row of an OpenCode todowrite call.
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
  cumulativeCalcCost: number;
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
  costByModel: MetricsCostByModel;
  stopReasons: StopReasonCount[];
  requests: RequestMetricsRow[];
  totalRequests: number;
  sessions: SessionLogEntry[];
  totalSessions: number;
  projects: ProjectLogEntry[];
  totalProjects: number;
}

/**
 * Per-model cumulative cost series used by the stacked cost-by-model
 * chart on the Stats tab. `models` is the ordered legend (top N by
 * total spend, with an "Other" bucket trailing when there are more
 * distinct models than the chart can show). `series` mirrors
 * `MetricsDashboard.series` bucket-for-bucket; each `costs` array is
 * parallel to `models`.
 */
export interface MetricsCostByModel {
  models: string[];
  series: ModelCostPoint[];
}

export interface ModelCostPoint {
  label: string;
  costs: number[];
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
  userMessages: number;
}

export interface ModelUsage {
  provider: string;
  model: string;
  count: number;
  tokensIn: number;
  tokensOut: number;
  cacheRead: number;
  cacheWrite: number;
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
   * Whether the platform can execute raw shell commands directly,
   * bypassing the LLM (OpenCode's POST /session/{id}/shell). Drives
   * the composer's `!`-prefix routing: when false, `!`-prefixed
   * input is sent as a plain prompt instead.
   */
  shellExec: boolean;
	/**
	 * Whether the platform exposes /api/session/{id}/changes — the
	 * per-file change aggregation used by the session-changes sidebar.
	 * False for adapters that can't compute a useful summary.
	 */
  fileChanges: boolean;
	/**
	 * Whether the platform exposes /api/session/{id}/info — the
	 * per-session context-window / MCP / LSP snapshot used by the
	 * "Session info" right-hand panel. False for adapters that can't
	 * produce a useful snapshot.
	 */
  sessionInfo: boolean;
  /**
   * Short, user-facing message explaining how to establish the live
   * connection to a running agent instance when it's missing (e.g.
   * "Start OpenCode with `opencode --port 0` ..." for OpenCode).
   * Empty / absent when the platform has no such setup step.
   */
  liveConnectionHint?: string;
  /**
   * Whether this platform supports per-session auto-approve:
   * GET/POST /api/session/{id}/auto-approve.
   * When true the permission prompt shows an auto-approve toggle and
   * a "Checking..." indicator while the server-side judge runs.
   */
  autoApprove: boolean;
}

export interface PlatformCapabilityEntry {
  id: string;
  displayName: string;
  available: boolean;
  capabilities: PlatformCapabilities;
}

export interface CapabilitiesResponse {
  platforms: PlatformCapabilityEntry[];
  /**
   * Server-wide flag for the /wt (worktree-sessions) feature. True
   * when (a) at least one OpenCode adapter is registered AND (b) git,
   * tmux, and opencode are all on the host's PATH. False otherwise —
   * the frontend uses this to hide the palette command, the project-
   * page link, and the per-project Worktrees view (AD-7).
   */
  worktreeSessions?: boolean;
}

/**
 * One row from `git worktree list --porcelain`, mirroring the Go
 * type internal/worktree.Entry on the wire.
 */
export interface WorktreeEntry {
  path: string;
  branch: string; // empty for detached HEAD
  head: string;
  bare: boolean;
  locked: boolean;
  main: boolean; // true for the primary worktree
}

/**
 * Request body for POST /api/worktree/create-and-launch.
 * `baseRef` is required when `newBranch` is true.
 */
export interface WorktreeCreateRequest {
  projectDir: string;
  branch: string;
  newBranch: boolean;
  baseRef?: string;
}

/**
 * Response from POST /api/worktree/create-and-launch.
 *
 * - `reused` is true when the target worktree already existed for
 *   the same branch (idempotent re-run).
 * - `branchExisted` is true when the caller asked to create a new
 *   branch but one with that name already existed locally, so the
 *   backend fell back to checking it out instead. The UI should warn
 *   the user that they're working on a pre-existing branch.
 * - `opencodeLaunched` is false when the tmux session pre-existed
 *   and we skipped the relaunch (AD-4). The user can still attach
 *   to the existing session via tmuxSession.
 */
export interface WorktreeCreateResponse {
  worktreePath: string;
  branch: string;
  reused: boolean;
  branchExisted: boolean;
  tmuxSession: string;
  tmuxTarget?: string;
  opencodeLaunched: boolean;
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
// can be starred independently across platforms.
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
