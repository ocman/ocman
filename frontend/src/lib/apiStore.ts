import { create } from 'zustand';
import { api } from './api';
import { getSidebarSessions, patchSessionLists, seedSessionLists } from './sessionsCache';
import type {
  DirectoryBrowseResponse,
  DirectorySearchResponse,
  ModelUsage,
  PermissionRequest,
  QuestionRequest,
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

/**
 * A session that was just archived ("closed") and can be reopened via the
 * Alt+Shift+N shortcut. Carries everything needed to unarchive and navigate
 * back to it without re-fetching first.
 */
export type ClosedSession = {
  platform: string;
  id: string;
  timeUpdated: number;
};

/**
 * Maximum number of recently-closed sessions to remember for the
 * "reopen last closed" shortcut. A small stack lets the user reopen
 * several in a row without unbounded growth.
 */
export const CLOSED_SESSION_STACK_MAX = 10;

/**
 * Maximum number of session detail responses to keep in the client-side
 * cache. Entries are evicted in LRU order when this limit is exceeded.
 * useSession stores only the newest page of each session (`latestPage`), so
 * an entry stays small; 10 covers recent navigation plus sessions warmed by
 * a sidebar hover. See spec/session-switch-cache/architecture.md.
 */
export const SESSION_CACHE_MAX = 10;

let whisperStatus: Promise<{ available: boolean }> | null = null;

/** Test hook: forget the cached whisper status. */
export function resetWhisperStatusCache() {
  whisperStatus = null;
}

type ApiStore = {
  pendingInterruptionReads: Record<string, { timeUpdated: number; confirmed?: boolean }>;
  /**
   * Per-row status write counters for the sidebar list (see
   * lib/sessionsCache). A poll that started before an optimistic status
   * write keeps the newer status instead of overwriting it.
   */
  recentStatusRevisions: Record<string, number>;
  /**
   * Stack of recently-closed (archived) sessions, most-recent last. Pushed
   * by the archive call sites and popped by the "reopen last closed session"
   * shortcut (Alt+Shift+N). Capped at CLOSED_SESSION_STACK_MAX.
   */
  closedSessionStack: ClosedSession[];
  pushClosedSession: (session: ClosedSession) => void;
  popClosedSession: () => ClosedSession | null;
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
  seedNewSession: (id: string, directory: string, platform: string, title?: string, remoteId?: string) => void;
  getProjects: (signal?: AbortSignal) => Promise<Project[]>;
  browseDirectories: (directory?: string, signal?: AbortSignal) => Promise<DirectoryBrowseResponse>;
  searchDirectories: (root: string | undefined, query: string, limit?: number, signal?: AbortSignal) => Promise<DirectorySearchResponse>;
  getSessions: (params?: { dir?: string; since?: number; limit?: number }, signal?: AbortSignal) => Promise<Session[]>;
  getSession: (id: string, limit?: number, offset?: number, signal?: AbortSignal) => Promise<SessionDetail>;
  /** Reads a session without opening it, so it is not unarchived. */
  peekSession: (id: string, signal?: AbortSignal, platform?: string) => Promise<SessionDetail>;
  getSessionChanges: (id: string, signal?: AbortSignal) => Promise<SessionChanges>;
  getSessionInfo: (id: string, signal?: AbortSignal, platform?: string) => Promise<SessionInfo>;
  getGitDiff: (dir: string, opts?: { fresh?: boolean }, signal?: AbortSignal) => Promise<WorkingTreeDiff>;
  archiveSession: (platform: string, sessionId: string, timeUpdated: number, archived?: boolean) => Promise<{ ok: boolean }>;
  archiveProject: (directory: string, archived?: boolean, remoteId?: string) => Promise<{ ok: boolean }>;
  markSessionSeen: (platform: string, sessionId: string, timeUpdated: number, interrupted?: boolean) => Promise<{ ok: boolean }>;
  pinSession: (platform: string, sessionId: string, pinned: boolean) => Promise<{ ok: boolean }>;
  getModels: (signal?: AbortSignal) => Promise<ModelUsage[]>;
  sendMessage: (sessionId: string, message: string, images?: { url: string; mime: string }[], model?: string, agent?: string, reasoning?: string, platform?: string, queue?: boolean) => Promise<void>;
  listPermissions: (sessionId: string) => Promise<PermissionRequest[]>;
  refreshPermissions: (sessionId: string, platform: string) => Promise<PermissionRequest[]>;
  respondPermission: (sessionId: string, permissionId: string, reply: 'once' | 'always' | 'reject') => Promise<void>;
  listQuestions: (sessionId: string) => Promise<QuestionRequest[]>;
  respondQuestion: (sessionId: string, requestId: string, answers: string[][]) => Promise<void>;
  rejectQuestion: (sessionId: string, requestId: string) => Promise<void>;
  getAutoApprove: (sessionId: string) => Promise<{ enabled: boolean; overridden: boolean }>;
  setAutoApprove: (sessionId: string, enabled: boolean) => Promise<void>;
  getPromptSections: () => Promise<Array<{ title: string; content: string; enabled?: boolean }>>;
  setPromptSectionsApi: (
    sections: Array<{ title: string; content: string; enabled?: boolean }>,
  ) => Promise<void>;
  getJudgeDelay: () => Promise<number>;
  setJudgeDelayApi: (delayMs: number) => Promise<void>;
  getJudgeModel: () => Promise<string>;
  setJudgeModelApi: (model: string) => Promise<void>;
  getJudgeModelOptions: (signal?: AbortSignal) => Promise<{ models: string[]; default: string }>;
  abortSession: (sessionId: string) => Promise<void>;
  getTmuxClients: (signal?: AbortSignal) => Promise<{ available: boolean; clients: TmuxClient[] }>;
  getTmuxSessions: (signal?: AbortSignal) => Promise<{ available: boolean; sessions: TmuxSession[] }>;
  switchTmuxSession: (session: string, client?: string) => Promise<void>;
  launchOpencodeInTmux: (directory: string, remoteId?: string) => Promise<{ session: string }>;
  getWhisperStatus: (signal?: AbortSignal) => Promise<{ available: boolean }>;
  transcribe: (audio: Blob) => Promise<string>;
  getSystemStats: (signal?: AbortSignal) => Promise<SystemStats>;
};

export const useApiStore = create<ApiStore>((set, get) => ({
  pendingInterruptionReads: {},
  recentStatusRevisions: {},
  closedSessionStack: [],
  pushClosedSession: (session) => {
    set((state) => {
      // Drop any earlier entry for the same session so reopening then
      // re-closing keeps it at the top of the stack.
      const filtered = state.closedSessionStack.filter((s) => s.id !== session.id);
      const next = [...filtered, session];
      if (next.length > CLOSED_SESSION_STACK_MAX) {
        next.splice(0, next.length - CLOSED_SESSION_STACK_MAX);
      }
      return { closedSessionStack: next };
    });
  },
  popClosedSession: () => {
    const stack = get().closedSessionStack;
    if (stack.length === 0) return null;
    const session = stack[stack.length - 1];
    set({ closedSessionStack: stack.slice(0, -1) });
    return session;
  },
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
  seedNewSession: (id, directory, platform, title, remoteId) => {
    const now = Date.now();
    const stub: SessionDetail = {
      session: {
        id,
        platform,
        projectId: '',
        title: title ?? '',
        directory,
        remoteId,
        timeCreated: now,
        timeUpdated: now,
        summaryAdditions: null,
        summaryDeletions: null,
        summaryFiles: null,
        shareUrl: null,
        messageCount: 0,
        durationMs: 0,
        activeDurationMs: 0,
        totalInputTokens: 0,
        totalOutputTokens: 0,
        totalCost: 0,
        status: 'waiting',
        // Seeding follows a successful launch; the first detail fetch reconciles it.
        liveConnection: true,
        pendingPermission: false,
        pendingQuestion: false,
        archived: false,
        seen: false,
        pinned: false,
        pinnedAt: 0,
        seenTimeUpdated: 0,
        unreadCount: 0,
      },
      messages: [],
      parts: [],
      totalMessages: 0,
    };
    // Seed the detail cache so SessionDetail skips the loading spinner.
    get().setCachedSession(id, stub);
    // Show the stub in the sidebar and every loaded list immediately
    // instead of waiting for a poll tick.
    seedSessionLists(stub.session);
  },
  getProjects: (signal) => api.projects(signal),
  browseDirectories: (directory, signal) => api.browseDirectories(directory, signal),
  searchDirectories: (root, query, limit, signal) => api.searchDirectories(root, query, limit, signal),
  getSessions: (params, signal) => api.sessions(params, signal),
  getSession: (id, limit = 50, offset = 0, signal) => api.session(id, limit, offset, signal),
  peekSession: (id, signal, platform) => api.session(id, 1, 0, signal, platform, true),
  getSessionChanges: (id, signal) => api.sessionChanges(id, signal),
  getSessionInfo: (id, signal, platform) => api.sessionInfo(id, signal, platform),
  getGitDiff: (dir, opts, signal) => api.gitDiff(dir, opts, signal),
  archiveSession: (platform, sessionId, timeUpdated, archived = true) => api.archiveSession(platform, sessionId, timeUpdated, archived),
  archiveProject: (directory, archived = true, remoteId) => api.archiveProject(directory, archived, remoteId),
  markSessionSeen: async (platform, sessionId, timeUpdated, interrupted) => {
    const key = `${platform}:${sessionId}`;
    const pending = { timeUpdated };
    let succeeded = false;
    if (interrupted) set(state => ({ pendingInterruptionReads: { ...state.pendingInterruptionReads, [key]: pending } }));
    try {
      const result = await api.markSessionSeen(platform, sessionId, timeUpdated, interrupted);
      succeeded = true;
      const row = getSidebarSessions().find(s => s.platform === platform && s.id === sessionId);
      if (interrupted && row?.status === 'interrupted' && row.timeUpdated <= timeUpdated) {
        patchSessionLists(sessionId, { seen: true, seenTimeUpdated: Math.max(row.seenTimeUpdated, timeUpdated) }, platform);
      }
      return result;
    } finally {
      if (interrupted) set(state => {
        if (state.pendingInterruptionReads[key] !== pending) return state;
        if (succeeded && !getSidebarSessions().some(s => s.platform === platform && s.id === sessionId)) {
          return { pendingInterruptionReads: { ...state.pendingInterruptionReads, [key]: { timeUpdated, confirmed: true } } };
        }
        const next = { ...state.pendingInterruptionReads };
        delete next[key];
        return { pendingInterruptionReads: next };
      });
    }
  },
  pinSession: (platform, sessionId, pinned) => api.pinSession(platform, sessionId, pinned),
  getModels: (signal) => api.models(undefined, signal),
  sendMessage: (sessionId, message, images, model, agent, reasoning, platform, queue) => {
    const owns = (s: Session) => s.id === sessionId && (!platform || s.platform === platform);
    const previous = getSidebarSessions().find(owns)?.timeUpdated;
    const now = Math.max(Date.now(), previous ?? 0);
    if (!queue) patchSessionLists(sessionId, { timeUpdated: now }, platform);
    return api.sendMessage(sessionId, message, images, model, agent, reasoning, platform, queue).catch((error) => {
      // Don't undo activity that arrived through SSE while the request ran.
      if (!queue && previous !== undefined &&
          getSidebarSessions().find(owns)?.timeUpdated === now) {
        patchSessionLists(sessionId, { timeUpdated: previous }, platform);
      }
      throw error;
    });
  },
  listPermissions: (sessionId) => api.listPermissions(sessionId),
  refreshPermissions: (sessionId, platform) => api.refreshPermissions(sessionId, platform),
  respondPermission: (sessionId, permissionId, reply) => api.respondPermission(sessionId, permissionId, reply),
  listQuestions: (sessionId) => api.listQuestions(sessionId),
  respondQuestion: (sessionId, requestId, answers) => api.respondQuestion(sessionId, requestId, answers),
  rejectQuestion: (sessionId, requestId) => api.rejectQuestion(sessionId, requestId),
  getAutoApprove: (sessionId) => api.getAutoApprove(sessionId),
  setAutoApprove: (sessionId, enabled) => api.setAutoApprove(sessionId, enabled),
  getPromptSections: () => api.getPromptSections(),
  setPromptSectionsApi: (sections) => api.setPromptSections(sections),
  getJudgeDelay: () => api.getJudgeDelay(),
  setJudgeDelayApi: (delayMs) => api.setJudgeDelay(delayMs),
  getJudgeModel: () => api.getJudgeModel(),
  setJudgeModelApi: (model) => api.setJudgeModel(model),
  getJudgeModelOptions: (signal) => api.getJudgeModelOptions(signal),
  abortSession: (sessionId) => api.abortSession(sessionId),
  getTmuxClients: (signal) => api.tmuxClients(signal),
  getTmuxSessions: (signal) => api.tmuxSessions(signal),
  switchTmuxSession: (session, client) => api.tmuxSwitch(session, client),
  launchOpencodeInTmux: (directory, remoteId) => api.tmuxLaunchOpencode(directory, remoteId),
  getWhisperStatus: (signal) => {
    // Whisper availability is machine state, so one successful read per
    // app load serves every session switch. A failure is not cached.
    whisperStatus ??= api.whisperStatus(signal)
      .catch((err) => { whisperStatus = null; throw err; });
    return whisperStatus;
  },
  transcribe: (audio) => api.transcribe(audio),
  getSystemStats: (signal) => api.systemStats(signal),
}));

