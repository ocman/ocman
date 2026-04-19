export async function fetchJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const resp = await fetch(url, signal ? { signal } : undefined);
  if (!resp.ok) throw new Error(await resp.text());
  return resp.json();
}

/**
 * Minimal per-session projection returned by /api/sessions/notify.
 * Only sessions that could drive the favicon/title notification state
 * are included in the response, so the caller can simply check whether
 * the array is non-empty to decide whether to show a badge.
 */
export interface NotifyEntry {
  id: string;
  status: string;
  seen: boolean;
  pendingPermission?: boolean;
  pendingQuestion?: boolean;
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
  reasoning?: string[];
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
  metrics: (params?: { agent?: string; model?: string; days?: number; limit?: number; offset?: number; sessionLimit?: number; sessionOffset?: number }) => {
    const q = new URLSearchParams();
    if (params?.agent) q.set('agent', params.agent);
    if (params?.model) q.set('model', params.model);
    if (params?.days != null) q.set('days', String(params.days));
    if (params?.limit != null) q.set('limit', String(params.limit));
    if (params?.offset != null) q.set('offset', String(params.offset));
    if (params?.sessionLimit != null) q.set('sessionLimit', String(params.sessionLimit));
    if (params?.sessionOffset != null) q.set('sessionOffset', String(params.sessionOffset));
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
  createSession: async (directory: string, platform?: string) => {
    const resp = await fetch('/api/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ directory, ...(platform ? { platform } : {}) }),
    });
    if (!resp.ok) throw new Error(await resp.text());
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
};
