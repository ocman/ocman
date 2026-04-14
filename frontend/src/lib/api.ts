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
