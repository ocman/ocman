import { create } from 'zustand';
import { api } from './api';
import type {
  CapabilitiesResponse,
  MetricsDashboard,
  ModelUsage,
  Project,
  Session,
  SessionDetail,
  SystemStats,
  SessionChanges,
  SessionInfo,
  TmuxClient,
  TmuxSession,
  WorkingTreeDiff,
} from './api';

type RequestStatus = {
  loading: boolean;
  error: string | null;
};

/**
 * Maximum number of session detail responses to keep in the client-side
 * cache. Entries are evicted in LRU order when this limit is exceeded.
 * Kept small because each entry can hold hundreds of messages and their
 * parts; 3 is enough for back-and-forth navigation without unbounded
 * memory growth. See spec/session-switch-cache/architecture.md.
 */
export const SESSION_CACHE_MAX = 3;

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
  /**
   * Plant a minimal SessionDetail stub for a brand-new session so that
   * navigating to /session/<id> renders an empty thread immediately
   * instead of showing the "Loading conversation…" spinner while the
   * first getSession fetch completes.  The real payload will overwrite
   * this stub as soon as the fetch resolves.
   */
  seedNewSession: (id: string, directory: string, platform: string, title?: string) => void;
  runRequest: <T>(key: string, task: () => Promise<T>) => Promise<T>;
  getMetrics: (params?: { agent?: string; model?: string; days?: number }, signal?: AbortSignal) => Promise<MetricsDashboard>;
  getProjects: (signal?: AbortSignal) => Promise<Project[]>;
  getSessions: (params?: { dir?: string; since?: number; limit?: number }, signal?: AbortSignal) => Promise<Session[]>;
  refreshCachedSessions: (signal?: AbortSignal) => Promise<Session[]>;
  getSession: (id: string, limit?: number, offset?: number, signal?: AbortSignal) => Promise<SessionDetail>;
  getSessionChanges: (id: string, signal?: AbortSignal) => Promise<SessionChanges>;
  getSessionInfo: (id: string, signal?: AbortSignal) => Promise<SessionInfo>;
  getGitDiff: (dir: string, opts?: { fresh?: boolean }, signal?: AbortSignal) => Promise<WorkingTreeDiff>;
  archiveSession: (platform: string, sessionId: string, timeUpdated: number, archived?: boolean) => Promise<{ ok: boolean }>;
  markSessionSeen: (platform: string, sessionId: string, timeUpdated: number) => Promise<{ ok: boolean }>;
  pinSession: (platform: string, sessionId: string, pinned: boolean) => Promise<{ ok: boolean }>;
  getModels: (signal?: AbortSignal) => Promise<ModelUsage[]>;
  getCapabilities: (signal?: AbortSignal) => Promise<CapabilitiesResponse>;
  createSession: (directory: string, platform?: string, title?: string) => Promise<{ id: string }>;
  sendMessage: (sessionId: string, message: string, images?: { url: string; mime: string }[], model?: string, agent?: string, reasoning?: string) => Promise<void>;
  listPermissions: (sessionId: string) => Promise<unknown[]>;
  respondPermission: (sessionId: string, permissionId: string, reply: 'once' | 'always' | 'reject') => Promise<void>;
  listQuestions: (sessionId: string) => Promise<unknown[]>;
  respondQuestion: (sessionId: string, requestId: string, answers: string[][]) => Promise<void>;
  rejectQuestion: (sessionId: string, requestId: string) => Promise<void>;
  abortSession: (sessionId: string) => Promise<void>;
  getTmuxClients: (signal?: AbortSignal) => Promise<{ available: boolean; clients: TmuxClient[] }>;
  getTmuxSessions: (signal?: AbortSignal) => Promise<{ available: boolean; sessions: TmuxSession[] }>;
  switchTmuxSession: (session: string, client?: string) => Promise<void>;
  launchOpencodeInTmux: (directory: string) => Promise<{ session: string }>;
  getWhisperStatus: (signal?: AbortSignal) => Promise<{ available: boolean }>;
  transcribe: (audio: Blob) => Promise<string>;
  getSystemStats: (signal?: AbortSignal) => Promise<SystemStats>;
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
  seedNewSession: (id, directory, platform, title) => {
    const now = Date.now();
    const stub: SessionDetail = {
      session: {
        id,
        platform,
        projectId: '',
        title: title ?? '',
        directory,
        timeCreated: now,
        timeUpdated: now,
        summaryAdditions: null,
        summaryDeletions: null,
        summaryFiles: null,
        shareUrl: null,
        messageCount: 0,
        durationMs: 0,
        totalInputTokens: 0,
        totalOutputTokens: 0,
        totalCost: 0,
        status: 'waiting',
        liveConnection: false,
        pendingPermission: false,
        pendingQuestion: false,
        archived: false,
        seen: false,
        pinned: false,
        pinnedAt: 0,
      },
      messages: [],
      parts: [],
      totalMessages: 0,
    };
    // Seed the detail cache so SessionDetail skips the loading spinner.
    get().setCachedSession(id, stub);
    // Prepend the stub to cachedSessions so sidebar renders it immediately.
    set((state) => ({
      cachedSessions: state.cachedSessions
        ? [stub.session, ...state.cachedSessions.filter((s) => s.id !== id)]
        : [stub.session],
    }));
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
      // Aborted requests are not real failures — don't write an error
      // state for them so the UI doesn't flash error messages during
      // rapid navigation or filter changes (P4 fix).
      if (error instanceof DOMException && error.name === 'AbortError') {
        throw error;
      }
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
  getMetrics: (params, signal) => get().runRequest(`metrics:get:${params?.agent ?? ''}:${params?.model ?? ''}:${params?.days ?? ''}`, () => api.metrics(params, signal)),
  getProjects: (signal) => get().runRequest('projects:get', () => api.projects(signal)),
  getSessions: (params, signal) => {
    // Stable key so useApiRequest('sessions:get') sees status transitions.
    // Per-params variance (since, limit) doesn't need its own loading row —
    // the dashboard only cares about the latest request's status.
    const key = params?.dir ? `sessions:get:dir:${params.dir}` : 'sessions:get';
    const isFullList = !params?.dir && !params?.since && !params?.limit;
    return get().runRequest(key, async () => {
      const result = await api.sessions(params, signal);
      // Opportunistically keep the shared cache warm whenever the full,
      // unfiltered session list is fetched — e.g. when the Dashboard loads.
      if (isFullList) set({ cachedSessions: result });
      return result;
    });
  },
  refreshCachedSessions: (signal) => {
    const since = Date.now() - 12 * 60 * 60 * 1000;
    return get().runRequest('sessions:get', async () => {
      const result = await api.sessions({ since, limit: 100 }, signal);
      set({ cachedSessions: result });
      return result;
    });
  },
  getSession: (id, limit = 50, offset = 0, signal) => get().runRequest(`session:get:${id}`, () => api.session(id, limit, offset, signal)),
  getSessionChanges: (id, signal) => get().runRequest(`session:changes:${id}`, () => api.sessionChanges(id, signal)),
  getSessionInfo: (id, signal) => get().runRequest(`session:info:${id}`, () => api.sessionInfo(id, signal)),
  getGitDiff: (dir, opts, signal) => get().runRequest(`git:diff:${dir}`, () => api.gitDiff(dir, opts, signal)),
  archiveSession: (platform, sessionId, timeUpdated, archived = true) => get().runRequest(`session:archive:${sessionId}`, () => api.archiveSession(platform, sessionId, timeUpdated, archived)),
  markSessionSeen: (platform, sessionId, timeUpdated) => get().runRequest(`session:seen:${sessionId}`, () => api.markSessionSeen(platform, sessionId, timeUpdated)),
  pinSession: (platform, sessionId, pinned) => get().runRequest(`session:pin:${sessionId}`, () => api.pinSession(platform, sessionId, pinned)),
  getModels: (signal) => get().runRequest('models:get', () => api.models(undefined, signal)),
  getCapabilities: (signal) => get().runRequest('capabilities:get', () => api.capabilities(signal)),
  createSession: (directory, platform, title) => get().runRequest('session:create', () => api.createSession(directory, platform, title)),
  sendMessage: (sessionId, message, images, model, agent, reasoning) => get().runRequest(`message:send:${sessionId}`, () => api.sendMessage(sessionId, message, images, model, agent, reasoning)),
  listPermissions: (sessionId) => get().runRequest(`permissions:list:${sessionId}`, () => api.listPermissions(sessionId)),
  respondPermission: (sessionId, permissionId, reply) => get().runRequest(`permission:respond:${sessionId}`, () => api.respondPermission(sessionId, permissionId, reply)),
  listQuestions: (sessionId) => get().runRequest(`questions:list:${sessionId}`, () => api.listQuestions(sessionId)),
  respondQuestion: (sessionId, requestId, answers) => get().runRequest(`question:respond:${sessionId}`, () => api.respondQuestion(sessionId, requestId, answers)),
  rejectQuestion: (sessionId, requestId) => get().runRequest(`question:reject:${sessionId}`, () => api.rejectQuestion(sessionId, requestId)),
  abortSession: (sessionId) => get().runRequest(`session:abort:${sessionId}`, () => api.abortSession(sessionId)),
  getTmuxClients: (signal) => get().runRequest('tmux-clients:get', () => api.tmuxClients(signal)),
  getTmuxSessions: (signal) => get().runRequest('tmux-sessions:get', () => api.tmuxSessions(signal)),
  switchTmuxSession: (session, client) => get().runRequest(`tmux:switch:${session}`, () => api.tmuxSwitch(session, client)),
  launchOpencodeInTmux: (directory) => get().runRequest(`tmux:launch-opencode:${directory}`, () => api.tmuxLaunchOpencode(directory)),
  getWhisperStatus: (signal) => get().runRequest('whisper-status:get', () => api.whisperStatus(signal)),
  transcribe: (audio) => get().runRequest('transcribe:post', () => api.transcribe(audio)),
  getSystemStats: (signal) => get().runRequest('system-stats:get', () => api.systemStats(signal)),
}));

const DEFAULT_REQUEST_STATUS: RequestStatus = { loading: true, error: null };

export function useApiRequest(key: string): RequestStatus {
  return useApiStore((state) => state.requests[key] ?? DEFAULT_REQUEST_STATUS);
}
