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

/**
 * Maximum number of session detail responses to keep in the client-side
 * cache. Entries are evicted in LRU order when this limit is exceeded.
 * Kept small because each entry can hold hundreds of messages and their
 * parts; 5 is enough for back-and-forth navigation without unbounded
 * memory growth. See spec/session-switch-cache/architecture.md.
 */
export const SESSION_CACHE_MAX = 5;

type ApiStore = {
  requests: Record<string, RequestStatus>;
  // Cached list of all sessions (no directory filter). `null` means never
  // fetched; components can render immediately from this value while
  // `refreshCachedSessions` updates it in the background.
  cachedSessions: Session[] | null;
  // LRU cache of session detail responses, used to render recently-viewed
  // sessions instantly on return. See spec/session-switch-cache.
  sessionCache: Map<string, SessionDetail>;
  sessionCacheOrder: string[];
  getCachedSession: (id: string) => SessionDetail | null;
  setCachedSession: (id: string, data: SessionDetail) => void;
  updateCachedSession: (id: string, updater: (prev: SessionDetail) => SessionDetail) => void;
  clearCachedSession: (id: string) => void;
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
  sessionCache: new Map(),
  sessionCacheOrder: [],
  getCachedSession: (id) => get().sessionCache.get(id) ?? null,
  setCachedSession: (id, data) => {
    set((state) => {
      const nextCache = new Map(state.sessionCache);
      nextCache.set(id, data);
      // Remove existing slot (if any) and push to the end (most-recent).
      const filteredOrder = state.sessionCacheOrder.filter((entry) => entry !== id);
      filteredOrder.push(id);
      // Evict oldest entries while we exceed the cap.
      while (filteredOrder.length > SESSION_CACHE_MAX) {
        const evicted = filteredOrder.shift();
        if (evicted !== undefined) nextCache.delete(evicted);
      }
      return { sessionCache: nextCache, sessionCacheOrder: filteredOrder };
    });
  },
  updateCachedSession: (id, updater) => {
    set((state) => {
      const existing = state.sessionCache.get(id);
      if (!existing) return state;
      const nextCache = new Map(state.sessionCache);
      nextCache.set(id, updater(existing));
      return { sessionCache: nextCache };
    });
  },
  clearCachedSession: (id) => {
    set((state) => {
      if (!state.sessionCache.has(id)) return state;
      const nextCache = new Map(state.sessionCache);
      nextCache.delete(id);
      const nextOrder = state.sessionCacheOrder.filter((entry) => entry !== id);
      return { sessionCache: nextCache, sessionCacheOrder: nextOrder };
    });
  },
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
