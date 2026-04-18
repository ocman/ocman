import { create } from 'zustand';
import { api } from './api';
import type {
  ActivityDay,
  HourlyData,
  HourlyTokensByModel,
  MetricsDashboard,
  ModelUsage,
  PortInfo,
  Project,
  Session,
  SessionDetail,
  Stats,
  TmuxClient,
  TmuxSession,
} from './api';

type RequestStatus = {
  loading: boolean;
  error: string | null;
};

type ApiStore = {
  requests: Record<string, RequestStatus>;
  // Cached list of all sessions (no directory filter). `null` means never
  // fetched; components can render immediately from this value while
  // `refreshCachedSessions` updates it in the background.
  cachedSessions: Session[] | null;
  runRequest: <T>(key: string, task: () => Promise<T>) => Promise<T>;
  getStats: () => Promise<Stats>;
  getMetrics: (params?: { agent?: string; model?: string; days?: number }) => Promise<MetricsDashboard>;
  getProjects: () => Promise<Project[]>;
  getSessions: (params?: { dir?: string; since?: number }, signal?: AbortSignal) => Promise<Session[]>;
  refreshCachedSessions: (signal?: AbortSignal) => Promise<Session[]>;
  getSession: (id: string, limit?: number, offset?: number, signal?: AbortSignal) => Promise<SessionDetail>;
  archiveSession: (sessionId: string, timeUpdated: number, archived?: boolean) => Promise<{ ok: boolean }>;
  markSessionSeen: (sessionId: string, timeUpdated: number) => Promise<{ ok: boolean }>;
  getActivity: () => Promise<ActivityDay[]>;
  getModels: () => Promise<ModelUsage[]>;
  getHourly: () => Promise<HourlyData[]>;
  getHourlyTokens: () => Promise<HourlyTokensByModel[]>;
  getSessionPort: (id: string, signal?: AbortSignal) => Promise<PortInfo>;
  createSession: (directory: string) => Promise<{ id: string }>;
  sendMessage: (sessionId: string, directory: string, message: string, images?: { url: string; mime: string }[], model?: string, agent?: string) => Promise<void>;
  listPermissions: (directory: string) => Promise<unknown[]>;
  respondPermission: (sessionId: string, directory: string, permissionId: string, reply: 'once' | 'always' | 'reject') => Promise<void>;
  listQuestions: (directory: string) => Promise<unknown[]>;
  respondQuestion: (sessionId: string, directory: string, requestId: string, answers: string[][]) => Promise<void>;
  rejectQuestion: (sessionId: string, directory: string, requestId: string) => Promise<void>;
  abortSession: (sessionId: string, directory: string) => Promise<void>;
  getTmuxClients: () => Promise<{ available: boolean; clients: TmuxClient[] }>;
  getTmuxSessions: () => Promise<{ available: boolean; sessions: TmuxSession[] }>;
  switchTmuxSession: (session: string, client?: string) => Promise<void>;
  getWhisperStatus: () => Promise<{ available: boolean }>;
  transcribe: (audio: Blob) => Promise<string>;
};

export const useApiStore = create<ApiStore>((set, get) => ({
  requests: {},
  cachedSessions: null,
  runRequest: async <T,>(key: string, task: () => Promise<T>) => {
    set((state) => ({
      requests: {
        ...state.requests,
        [key]: { loading: true, error: null },
      },
    }));

    try {
      const result = await task();
      set((state) => ({
        requests: {
          ...state.requests,
          [key]: { loading: false, error: null },
        },
      }));
      return result;
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Request failed';
      set((state) => ({
        requests: {
          ...state.requests,
          [key]: { loading: false, error: message },
        },
      }));
      throw error;
    }
  },
  getStats: () => get().runRequest('stats:get', () => api.stats()),
  getMetrics: (params) => get().runRequest(`metrics:get:${params?.agent ?? ''}:${params?.model ?? ''}:${params?.days ?? ''}`, () => api.metrics(params)),
  getProjects: () => get().runRequest('projects:get', () => api.projects()),
  getSessions: (params, signal) => {
    const key = params?.dir ? `sessions:get:dir:${params.dir}` : 'sessions:get';
    const isFullList = !params?.dir && !params?.since;
    return get().runRequest(key, async () => {
      const result = await api.sessions(params, signal);
      // Opportunistically keep the shared cache warm whenever the full,
      // unfiltered session list is fetched — e.g. when the Dashboard loads.
      if (isFullList) set({ cachedSessions: result });
      return result;
    });
  },
  refreshCachedSessions: (signal) =>
    get().runRequest('sessions:get', async () => {
      const result = await api.sessions(undefined, signal);
      set({ cachedSessions: result });
      return result;
    }),
  getSession: (id, limit = 50, offset = 0, signal) => get().runRequest(`session:get:${id}`, () => api.session(id, limit, offset, signal)),
  archiveSession: (sessionId, timeUpdated, archived = true) => get().runRequest(`session:archive:${sessionId}`, () => api.archiveSession(sessionId, timeUpdated, archived)),
  markSessionSeen: (sessionId, timeUpdated) => get().runRequest(`session:seen:${sessionId}`, () => api.markSessionSeen(sessionId, timeUpdated)),
  getActivity: () => get().runRequest('activity:get', () => api.activity()),
  getModels: () => get().runRequest('models:get', () => api.models()),
  getHourly: () => get().runRequest('hourly:get', () => api.hourly()),
  getHourlyTokens: () => get().runRequest('hourly-tokens:get', () => api.hourlyTokens()),
  getSessionPort: (id, signal) => get().runRequest(`session-port:get:${id}`, () => api.sessionPort(id, signal)),
  createSession: (directory) => get().runRequest('session:create', () => api.createSession(directory)),
  sendMessage: (sessionId, directory, message, images, model, agent) => get().runRequest(`message:send:${sessionId}`, () => api.sendMessage(sessionId, directory, message, images, model, agent)),
  listPermissions: (directory) => get().runRequest(`permissions:list:${directory}`, () => api.listPermissions(directory)),
  respondPermission: (sessionId, directory, permissionId, reply) => get().runRequest(`permission:respond:${sessionId}`, () => api.respondPermission(sessionId, directory, permissionId, reply)),
  listQuestions: (directory) => get().runRequest(`questions:list:${directory}`, () => api.listQuestions(directory)),
  respondQuestion: (sessionId, directory, requestId, answers) => get().runRequest(`question:respond:${sessionId}`, () => api.respondQuestion(sessionId, directory, requestId, answers)),
  rejectQuestion: (sessionId, directory, requestId) => get().runRequest(`question:reject:${sessionId}`, () => api.rejectQuestion(sessionId, directory, requestId)),
  abortSession: (sessionId, directory) => get().runRequest(`session:abort:${sessionId}`, () => api.abortSession(sessionId, directory)),
  getTmuxClients: () => get().runRequest('tmux-clients:get', () => api.tmuxClients()),
  getTmuxSessions: () => get().runRequest('tmux-sessions:get', () => api.tmuxSessions()),
  switchTmuxSession: (session, client) => get().runRequest(`tmux:switch:${session}`, () => api.tmuxSwitch(session, client)),
  getWhisperStatus: () => get().runRequest('whisper-status:get', () => api.whisperStatus()),
  transcribe: (audio) => get().runRequest('transcribe:post', () => api.transcribe(audio)),
}));

export function useApiRequest(key: string): RequestStatus {
  return useApiStore((state) => state.requests[key] ?? { loading: true, error: null });
}
