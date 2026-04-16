export async function fetchJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const resp = await fetch(url, signal ? { signal } : undefined);
  if (!resp.ok) throw new Error(await resp.text());
  return resp.json();
}

export interface Session {
  id: string;
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
  hasPort: boolean;
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
    tokens?: { input: number; output: number };
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
    metadata?: { description?: string };
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

export interface MetricsDashboard {
  availableAgents: string[];
  availableModels: string[];
  summary: MetricsSummary;
  series: MetricsPoint[];
  stopReasons: StopReasonCount[];
  requests: RequestMetricsRow[];
  totalRequests: number;
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

export interface PortInfo {
  port: string;
  available: boolean;
}

export interface SlashCommand {
  name: string;
  description?: string;
  agent?: string;
  model?: string;
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
  metrics: (params?: { agent?: string; model?: string; days?: number; limit?: number; offset?: number }) => {
    const q = new URLSearchParams();
    if (params?.agent) q.set('agent', params.agent);
    if (params?.model) q.set('model', params.model);
    if (params?.days != null) q.set('days', String(params.days));
    if (params?.limit != null) q.set('limit', String(params.limit));
    if (params?.offset != null) q.set('offset', String(params.offset));
    const qs = q.toString();
    return fetchJSON<MetricsDashboard>(`/api/metrics${qs ? '?' + qs : ''}`);
  },
  projects: () => fetchJSON<Project[]>('/api/projects'),
  sessions: (params?: { dir?: string; since?: number }, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (params?.dir) q.set('dir', params.dir);
    if (params?.since) q.set('since', String(params.since));
    const qs = q.toString();
    return fetchJSON<Session[]>(`/api/sessions${qs ? '?' + qs : ''}`, signal);
  },
  session: (id: string, limit = 50, offset = 0, signal?: AbortSignal) => fetchJSON<SessionDetail>(`/api/session/${id}?limit=${limit}&offset=${offset}`, signal),
  archiveSession: async (sessionId: string, timeUpdated: number, archived = true) => {
    const resp = await fetch('/api/session/archive', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, timeUpdated, archived }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    return resp.json() as Promise<{ ok: boolean }>;
  },
  markSessionSeen: async (sessionId: string, timeUpdated: number) => {
    const resp = await fetch('/api/session/seen', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, timeUpdated }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    return resp.json() as Promise<{ ok: boolean }>;
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
  sessionPort: (id: string, signal?: AbortSignal) => fetchJSON<PortInfo>(`/api/session-port/${id}`, signal),
  createSession: async (directory: string) => {
    const resp = await fetch('/api/create-session', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ directory }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    return resp.json() as Promise<{ id: string }>;
  },
  sendMessage: async (
    sessionId: string,
    directory: string,
    message: string,
    images?: { url: string; mime: string }[],
    model?: string,
    agent?: string,
  ) => {
    const resp = await fetch('/api/send-message', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, directory, message, images, model, agent }),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  listPermissions: (directory: string) => fetchJSON<unknown[]>(`/api/list-permissions?dir=${encodeURIComponent(directory)}`),
  respondPermission: async (
    sessionId: string,
    directory: string,
    permissionId: string,
    reply: 'once' | 'always' | 'reject',
  ) => {
    const resp = await fetch('/api/respond-permission', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, directory, permissionId, reply }),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  respondQuestion: async (
    sessionId: string,
    directory: string,
    requestId: string,
    answers: string[][],
  ) => {
    const resp = await fetch('/api/respond-question', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, directory, requestId, answers }),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  rejectQuestion: async (
    sessionId: string,
    directory: string,
    requestId: string,
  ) => {
    const resp = await fetch('/api/reject-question', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, directory, requestId }),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  abortSession: async (sessionId: string, directory: string) => {
    const resp = await fetch('/api/abort-session', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, directory }),
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
  compactSession: async (sessionId: string, directory: string, providerID: string, modelID: string) => {
    const resp = await fetch('/api/compact-session', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, directory, providerID, modelID }),
    });
    if (!resp.ok) throw new Error(await resp.text());
  },
  commands: (directory: string, signal?: AbortSignal) =>
    fetchJSON<SlashCommand[]>(`/api/commands?dir=${encodeURIComponent(directory)}`, signal),
  executeCommand: async (
    sessionId: string,
    directory: string,
    command: string,
    args: string,
    model?: string,
    agent?: string,
  ) => {
    const resp = await fetch('/api/command', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, directory, command, arguments: args, model, agent }),
    });
    if (!resp.ok) throw new Error(await resp.text());
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
