export async function fetchJSON<T>(url: string): Promise<T> {
  const resp = await fetch(url);
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
  status: 'waiting' | 'busy' | 'done';
  hasPort: boolean;
}

export interface Message {
  id: string;
  sessionId: string;
  timeCreated: number;
  data: {
    role: string;
    finish?: string;
    modelID?: string;
    cost?: number;
    tokens?: { input: number; output: number };
  };
}

export interface Part {
  id: string;
  messageId: string;
  sessionId: string;
  data: string | PartData;
}

export interface PartData {
  type: string;
  text?: string;
  tool?: string;
  state?: {
    status?: string;
    input?: Record<string, unknown>;
    output?: unknown;
    title?: string;
    metadata?: { description?: string };
  };
  file?: string;
  path?: string;
  content?: string;
  diff?: string;
}

export interface SessionDetail {
  session: Session;
  messages: Message[];
  parts: Part[];
  totalMessages?: number;
}

export interface Stats {
  totalSessions: number;
  totalMessages: number;
  totalProjects: number;
  totalTokensIn: number;
  totalTokensOut: number;
  totalCost: number;
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
  model: string;
  count: number;
  tokensIn: number;
  tokensOut: number;
}

export interface HourlyData {
  hour: number;
  sessions: number;
}

export interface PortInfo {
  port: string;
  available: boolean;
}

export const api = {
  stats: () => fetchJSON<Stats>('/api/stats'),
  projects: () => fetchJSON<Project[]>('/api/projects'),
  sessions: (params?: { dir?: string; since?: number }) => {
    const q = new URLSearchParams();
    if (params?.dir) q.set('dir', params.dir);
    if (params?.since) q.set('since', String(params.since));
    const qs = q.toString();
    return fetchJSON<Session[]>(`/api/sessions${qs ? '?' + qs : ''}`);
  },
  session: (id: string, limit = 50, offset = 0) => fetchJSON<SessionDetail>(`/api/session/${id}?limit=${limit}&offset=${offset}`),
  activity: () => fetchJSON<ActivityDay[]>('/api/activity'),
  models: () => fetchJSON<ModelUsage[]>('/api/models'),
  hourly: () => fetchJSON<HourlyData[]>('/api/hourly'),
  sessionPort: (id: string) => fetchJSON<PortInfo>(`/api/session-port/${id}`),
  createSession: async (directory: string) => {
    const resp = await fetch('/api/create-session', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ directory }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    return resp.json() as Promise<{ id: string }>;
  },
  sendMessage: async (sessionId: string, directory: string, message: string) => {
    const resp = await fetch('/api/send-message', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, directory, message }),
    });
    if (!resp.ok) throw new Error(await resp.text());
    return resp.json();
  },
};
