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
  /**
   * Session this one descends from, when any. Populated from either
   * OpenCode's `session.parent_id` (subagent sessions) or ocman's
   * `child_sessions.parent_session_id` (sessions spawned via the MCP
   * split tools). Empty/undefined for top-level sessions. Drives the
   * parent/child nesting in the session lists (see `nestSessions`).
   */
  parentId?: string;
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
   * The session's timeUpdated at the moment the user last viewed it.
   * Zero when the user has never opened the session. Used to compute
   * the "first unread" marker and unread badge on the session list
   * without an extra round-trip.
   */
  seenTimeUpdated: number;
  /**
   * Number of messages in this session with timeCreated strictly
   * greater than seenTimeUpdated. Zero when the session is fully
   * seen, when the platform doesn't expose per-message timestamps,
   * or when the count is suppressed for performance.
   */
  unreadCount: number;
  /**
   * Normalized, platform-agnostic explanation of a transient session
   * condition (e.g. rate-limit backoff). Absent when no notice applies.
   * The frontend renders this as a banner / tooltip without inspecting
   * the platform field.
   */
  notice?: SessionNotice;
  /**
   * Display-only host attributes for multi-remote support. remoteId is
   * 'local' for the hub's own machine, else the remote's random ID;
   * remoteName is the host label ('This machine' for local). The UI
   * renders remoteName as a host badge but must NOT branch behaviour on
   * these values — host capabilities come from /api/capabilities.
   */
  remoteId?: string;
  remoteName?: string;
  /** True when the row is last-known data from an offline remote. */
  stale?: boolean;
}

/**
 * A normalized session notice surfaced by the backend when the latest
 * assistant error matches a known transient pattern.
 */
export interface SessionNotice {
  kind: 'rate_limit' | 'provider_overloaded' | string;
  /** User-facing summary of the condition. */
  message: string;
  /** Unix ms timestamp when retry is expected, or 0 when unknown. */
  retryAt: number;
  /** Retry attempt number, or 0 when unknown. */
  attempt: number;
}

/**
 * Non-blocking condition attached to a full session detail response.
 * Warnings explain ambiguity that may affect live behavior while leaving
 * normal session reads and composer actions available.
 */
export interface SessionWarning {
  kind: 'duplicate_opencode_servers' | string;
  message: string;
  ports?: string[];
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
      output?: unknown;
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
  warnings?: SessionWarning[];
}

/**
 * A public, read-only share link for a session's conversation. The
 * token is embedded in `url`; anyone with that URL can view the
 * conversation without authenticating.
 */
export interface ShareLink {
  token: string;
  url: string;
  createdAt: number;
  expiresAt?: number;
}

/**
 * A share link augmented with its owning platform + session, returned
 * by the global GET /api/shares list used in Settings.
 */
export interface GlobalShareLink extends ShareLink {
  platform: string;
  sessionId: string;
}

/**
 * Public conversation payload returned by GET /api/share/{token}.
 * Deliberately a subset of SessionDetail — no live/actionable fields,
 * just enough to render the conversation read-only.
 */
export interface SharedConversation {
  session: Session | null;
  messages: Message[];
  parts: Part[];
  readOnly: boolean;
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
  /**
   * Headline cost: per request, the platform-reported cost when it's
   * non-zero, otherwise the token-derived estimate. Reconciles
   * subscription-plan sessions (reported $0) with API-priced ones so
   * the summary matches the per-row tables.
   */
  totalEffectiveCost: number;
}

export interface MetricsPoint {
  label: string;
  avgOutputTokensSec: number;
  cumulativeCost: number;
  cumulativeCalcCost: number;
  cumulativeEffectiveCost: number;
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
  effectiveCost: number;
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
  effectiveCost: number;
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
  effectiveCost: number;
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
  /** True when the project's folded root is archived (set server-side). */
  archived?: boolean;
  /** Set for projects that live on a connected remote (empty for local). */
  remoteId?: string;
  remoteName?: string;
  /** Compound platform id (r-<remoteId>:<base>) for remote projects. */
  platform?: string;
}

export interface DirectoryBrowseEntry {
  name: string;
  path: string;
  hidden?: boolean;
}

export interface DirectoryBrowseResponse {
  directory: string;
  parent?: string;
  home?: string;
  entries: DirectoryBrowseEntry[];
}

export interface DirectorySearchEntry {
  name: string;
  path: string;
  hidden?: boolean;
  project?: boolean;
  depth?: number;
}

export interface DirectorySearchResponse {
  root: string;
  query: string;
  entries: DirectorySearchEntry[];
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
  /**
   * Whether the platform supports reading/writing a per-session
   * permission ruleset (GET/PUT /api/session/{id}/permission-rules).
   * Gates the permission-mode lock in the session header.
   */
  permissionRules: boolean;
}

/**
 * One entry in a session's permission ruleset. Mirrors
 * internal/platforms.PermissionRule.
 */
export interface PermissionRule {
  permission: string;
  pattern: string;
  action: 'allow' | 'deny' | 'ask';
}

export interface PlatformCapabilityEntry {
  id: string;
  displayName: string;
  available: boolean;
  capabilities: PlatformCapabilities;
  /** Present only for remote platforms (multi-remote support). */
  remoteId?: string;
  remoteName?: string;
}

/** Directory-scoped host capabilities (multi-remote support, AD-16). */
export interface HostCapabilities {
  gitDiff: boolean;
  worktrees: boolean;
  tmux: boolean;
  projects: boolean;
  whisper: boolean;
}

/** One machine's host capabilities, grouped under its host identity. */
export interface HostCapabilityEntry {
  remoteId: string;
  remoteName: string;
  capabilities: HostCapabilities;
}

export interface CapabilitiesResponse {
  platforms: PlatformCapabilityEntry[];
  /**
   * Host-scoped capabilities per machine (additive; multi-remote
   * support). The frontend gates host UI on these flags, never on
   * remote identity.
   */
  hosts?: HostCapabilityEntry[];
  /**
   * Server-wide flag for the /wt (worktree-sessions) feature. True
   * when (a) at least one OpenCode adapter is registered AND (b) git,
   * tmux, and opencode are all on the host's PATH. False otherwise —
   * the frontend uses this to hide the palette command, the project-
   * page link, and the per-project Worktrees view (AD-7).
   */
  worktreeSessions?: boolean;
  /**
   * Agent-loops feature flag. enabled is true when the state DB is
   * present and the OpenCode adapter is available. The Loops view and
   * /loop palette command are gated on this.
   */
  agentLoops?: { enabled: boolean };
}

/** Decoded trigger config for an agent loop. */
export interface LoopTriggerConfig {
  interval_seconds?: number;
  cron_expr?: string;
  pr_number?: number;
  poll_seconds?: number;
  target_session_id?: string;
}

/** Decoded stop conditions for an agent loop. */
export interface LoopStopConditions {
  max_iterations: number;
  max_cost_usd?: number;
  max_tokens?: number;
  max_duration?: string;
  error_streak?: number;
  goal_predicate?: string;
}

/** A single agent loop (GET /api/loops, /api/loops/{id}). */
export interface Loop {
  id: string;
  platform: string;
  rootSessionID: string;
  directory: string;
  projectName: string;
  title: string;
  currentTask: string;
  pattern: string;
  triggerType: string;
  actionType: string;
  actionTemplate: string;
  model?: string;
  sessionMode: string; // 'fresh' | 'reuse'
  loopSessionID?: string;
  state: string;
  iteration: number;
  errorStreak: number;
  tokensUsed: number;
  costUSD: number;
  lastFiredAt: number;
  createdAt: number;
  updatedAt: number;
  completedAt: number;
  lastSummary: string;
  triggerConfig: LoopTriggerConfig;
  stopConditions: LoopStopConditions;
}

/** One iteration in a loop's audit trail. */
export interface LoopIteration {
  id: number;
  seq: number;
  firedAt: number;
  startedAt: number;
  completedAt: number;
  triggerDetail: string;
  renderedPrompt: string;
  targetSessionID: string;
  childSessionID: string;
  outcome: string;
  summary: string;
}

/** A session spawned by a loop (subset of state.ChildSession). */
export interface LoopChildSession {
  id: string;
  status: string; // starting, running, completed, error, cancelled
  createdAt: number;
  completedAt: number; // 0 until terminal
}

/** Loop detail = loop + iteration timeline + child sessions + sub-loops. */
export interface LoopDetail extends Loop {
  iterations: LoopIteration[];
  children: LoopChildSession[];
  subLoops: Loop[];
}

/** Create-loop request body (POST /api/loops). */
export interface LoopCreateRequest {
  // A loop anchors to either a root session (created from within a
  // session) or a project directory (created from the Loops page).
  root_session_id?: string;
  parent_loop_id?: string;
  platform?: string;
  title?: string;
  directory?: string;
  pattern?: string;
  trigger_type: string;
  trigger_config?: LoopTriggerConfig;
  action_type: string;
  action_template?: string;
  model?: string;
  stop_conditions: LoopStopConditions;
  session_mode?: string; // 'fresh' | 'reuse'
}

/**
 * Editable subset of a loop's settings (PATCH /api/loops/{id}). All
 * fields optional; only provided fields are changed. Trigger/action TYPE
 * are intentionally not editable.
 */
export interface LoopUpdateRequest {
  title?: string;
  action_template?: string;
  model?: string;
  session_mode?: string;
  trigger_config?: LoopTriggerConfig;
  stop_conditions?: LoopStopConditions;
}

/**
 * Hub-side view of one configured remote ocman instance
 * (GET /api/remotes). Tokens are never included.
 */
export interface RemoteStatus {
  localId: number;
  remoteId?: string;
  displayName: string;
  address: string;
  enabled: boolean;
  health: string;
  hostname: string;
  protocolVersion: number;
  lastSeen: number;
  sessionCount: number;
}

/**
 * One machine that has a project checked out, returned by
 * POST /api/sessions/resolve-targets for the new-session machine picker.
 * `platform` is the compound 'r-<id>:opencode' key for remotes (pass it
 * to createSession), or 'opencode' for the local machine.
 */
export interface TargetCandidate {
  remoteId: string;
  remoteName: string;
  platform: string;
  dir: string;
}

/** Response of POST /api/sessions/resolve-targets. */
export interface ResolveTargetsResponse {
  candidates: TargetCandidate[];
  remotes: TargetCandidate[];
}

/** This instance's own remote-access surface (GET /api/settings/remote-access). */
export interface RemoteAccessStatus {
  instanceId: string;
  listening: boolean;
  listenAddr: string;
  tls: boolean;
  tokenSet: boolean;
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
  /**
   * When set (and the worktree.inherit_permissions setting is on), the
   * new session inherits this session's accumulated always-allow
   * permissions at split time (#101).
   */
  parentSessionId?: string;
}

/**
 * Request body for POST /api/worktree/remove. `force` discards
 * uncommitted changes; without it the backend returns 409 for a dirty
 * worktree.
 */
export interface WorktreeRemoveRequest {
  projectDir: string;
  path: string;
  force?: boolean;
}

/**
 * Response from POST /api/worktree/create-and-launch.
 *
 * - `sessionId` is the in-app OpenCode session created on the project's
 *   single opencode instance, rooted at the worktree (#268). The UI
 *   navigates straight to it.
 * - `reused` is true when the target worktree already existed for
 *   the same branch (idempotent re-run).
 * - `branchExisted` is true when the caller asked to create a new
 *   branch but one with that name already existed locally, so the
 *   backend fell back to checking it out instead. The UI should warn
 *   the user that they're working on a pre-existing branch.
 *
 * Worktree sessions run in-app on the project's single opencode instance
 * (#265); there is no per-worktree tmux process to report.
 */
export interface WorktreeCreateResponse {
  sessionId: string;
  worktreePath: string;
  branch: string;
  reused: boolean;
  branchExisted: boolean;
  /**
   * True when the new session inherited one or more always-allow
   * permissions from the parent session (#101).
   */
  permissionsInherited?: boolean;
  /** Number of permission rules inherited from the parent (#101). */
  permissionsInheritedCount?: number;
  /**
   * Non-empty only when inheritance was attempted but failed; the
   * launch still succeeded (soft-fail).
   */
  permissionsInheritError?: string;
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

// QueuedMessage mirrors internal/server/handlers_session_queue.go:
// queuedMessageView — a follow-up prompt waiting for the session's next
// idle edge (#58).
export interface QueuedMessage {
  id: string;
  text: string;
  hasImages: boolean;
  model?: string;
  agent?: string;
  reasoning?: string;
  createdAt: number;
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

/**
 * A dedicated in-app terminal window. `name` is the tmux window
 * (`ocman-term-<slug>-<n>`); `title` is a display label derived from the
 * program-set pane title or running command (empty for an idle shell —
 * the UI falls back to the tab number).
 */
export interface TermWindow {
  name: string;
  title: string;
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
