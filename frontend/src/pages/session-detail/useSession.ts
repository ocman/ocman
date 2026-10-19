// Reducer-backed session view. Connection/loading and cache mirroring live in
// their own hooks; pagination and memory trimming stay with the view.
import { useCallback, useEffect, useLayoutEffect, useReducer, useRef, useState, type Dispatch } from 'react';
import { api, type Message, type Part, type SessionDetail } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { initialSessionView, type SessionAction, type SessionMetadata, type SessionView } from '../../lib/sessionReducer';
import { remoteLog } from '../../lib/remoteLog';
import { useActivityScope } from '../../lib/activityScopes';
import { markSseCommit } from '../../lib/perfMonitor';
import { computeReconnectDelay } from './sseBackoff';
import { reduceLiveSessionView } from '../../lib/conversation/liveSessionView';
import { platformMessageCount } from './sessionView';
import { useSessionCache } from './useSessionCache';
import { useSessionLifecycle } from './useSessionLifecycle';
import { patchSessionLists } from '../../lib/sessionsCache';

export { latestPage, platformMessageCount } from './sessionView';

export type UseSessionStatus = 'loading' | 'live' | 'reconnecting' | 'error';
export interface SseDebugEvent { at: number; event: string; data: string }

export interface UseSessionOptions {
  /** Owner supplied by an explicit deep link, authoritative over cached rows. */
  platform?: string;
  /** Tests inject fixtures through this seam; production uses api.session. */
  fetchSession?: (id: string, limit: number, offset: number, signal?: AbortSignal, platform?: string, peek?: boolean) => Promise<SessionDetail>;
  reconnectDelay?: (attempt: number) => number;
  /** Capture the last 50 raw events. Off by default. */
  debug?: boolean;
  /** Initial and pagination page size, default 30. */
  pageSize?: number;
  /** Maximum retained messages. 0 disables trimming. */
  maxMessages?: number;
  /** Floor after trimming, must be <= maxMessages. */
  trimTo?: number;
  /** Keep this message in memory while loading an explicit jump target. */
  protectedMessageId?: string | null;
}

export interface UseSessionResult extends SessionView {
  status: UseSessionStatus;
  /** Explicit retry replacing the view with authoritative state. */
  reload: () => Promise<void>;
  /** Reconcile a failed refresh while preserving streamed content. */
  refresh: () => Promise<void>;
  loadMore: () => Promise<void>;
  hydrateHistory: (messages: Message[], parts: Part[]) => void;
  loading: boolean;
  loadingMore: boolean;
  loadError: string | null;
  refreshing: boolean;
  refreshError: string | null;
  totalMessages: number;
  sseReconnecting: boolean;
  sseReconnectAttempt: number;
  sseNextRetryAt: number | null;
  retryNow: () => void;
  sseDebugEvents: SseDebugEvent[];
  changesDirtyTick: number;
  /** Clear only the currently displayed matching prompt. */
  clearPrompt: (kind: 'permission' | 'question', id: string) => void;
  /** Set-only reverse sync from sidebar prompts. */
  setPendingPermission: (perm: import('../../lib/sseHelpers').PendingPermission, ownerIds?: string[]) => void;
  setPendingQuestion: (q: import('../../components/session/QuestionPrompt').PendingQuestion) => void;
  patchSession: (patch: Partial<SessionMetadata>) => void;
  dispatch: Dispatch<SessionAction>;
}

async function defaultFetchSession(id: string, limit: number, offset: number, signal?: AbortSignal, platform?: string, peek = true) {
  return api.session(id, limit, offset, signal, platform, peek);
}

export function useSession(sessionId: string | undefined, options: UseSessionOptions = {}): UseSessionResult {
  useActivityScope(sessionId && sessionId !== 'new' ? `session:${sessionId}` : undefined);
  const { platform: requestedPlatform, fetchSession = defaultFetchSession, reconnectDelay = computeReconnectDelay,
    pageSize = 30, maxMessages = 0, trimTo = maxMessages, protectedMessageId = null, debug = false } = options;
  // Read a snapshot, not a subscription: cache writes must not restart the stream.
  const cachedCandidate = sessionId ? useApiStore.getState().getCachedSession(sessionId) : null;
  const cached = cachedCandidate && (!requestedPlatform || cachedCandidate.session.platform === requestedPlatform) ? cachedCandidate : null;
  // Pin the owner even if hover prefetches evict this entry from the LRU.
  const [owner, setOwner] = useState<{ id: string | undefined; platform: string | undefined }>({ id: sessionId, platform: undefined });
  const cachedPlatform = requestedPlatform || cached?.session.platform || (owner.id === sessionId ? owner.platform : undefined);
  if (owner.id !== sessionId || owner.platform !== cachedPlatform) setOwner({ id: sessionId, platform: cachedPlatform });
  const routedPlatform = requestedPlatform || (cachedPlatform?.startsWith('r-') ? cachedPlatform : undefined);
  const initialView: SessionView = cached ? {
    ...initialSessionView(sessionId!),
    _statusFresh: false,
    session: {
      ...cached.session,
      contextTokenCount: cached.session.contextTokenCount ?? cached.contextTokenCount,
      defaultAgent: cached.defaultAgent,
      defaultModel: cached.defaultModel,
      warnings: cached.warnings ?? [],
    },
    sessionTree: cached.sessionTree ?? [], messages: cached.messages, parts: cached.parts,
  } : initialSessionView(sessionId ?? '');
  const [view, dispatch] = useReducer(reduceLiveSessionView, initialView);
  const [loadingMore, setLoadingMore] = useState(false);
  const [totalMessages, setTotalMessages] = useState(cached?.totalMessages || cached?.session.messageCount || 0);
  const loadMoreAbortRef = useRef<AbortController | null>(null);
  const loadMoreInFlightRef = useRef(false);
  const viewRef = useRef(view);
  useLayoutEffect(() => { viewRef.current = view; markSseCommit(); }, [view]);

  const clearPrompt = useCallback((kind: 'permission' | 'question', id: string) => {
    dispatch({ type: 'clearPrompt', kind, id });
  }, []);
  const setPendingPermission = useCallback((permission: import('../../lib/sseHelpers').PendingPermission, ownerIds?: string[]) => {
    dispatch({ type: 'setPendingPermission', permission, ownerIds });
  }, []);
  const setPendingQuestion = useCallback((q: import('../../components/session/QuestionPrompt').PendingQuestion) => {
    dispatch({ type: 'setPendingQuestion', question: q });
  }, []);
  const patchSession = useCallback((patch: Partial<SessionMetadata>) => dispatch({ type: 'patchSession', patch }), []);

  const lastTrimmedLengthRef = useRef(0);
  useEffect(() => {
    if (maxMessages <= 0 || view.messages.length <= maxMessages || lastTrimmedLengthRef.current === view.messages.length) return;
    const protectedIndex = protectedMessageId ? view.messages.findIndex((m) => m.id === protectedMessageId) : -1;
    if (protectedMessageId && protectedIndex === -1) return;
    lastTrimmedLengthRef.current = view.messages.length;
    const retained = view.messages.slice(-trimTo);
    const retainedMessages = protectedIndex >= 0 && !retained.some((m) => m.id === protectedMessageId)
      ? [view.messages[protectedIndex], ...retained.slice(1)] : retained;
    const retainedIds = new Set(retainedMessages.map((m) => m.id));
    dispatch({ type: 'load', view: {
      ...view, messages: retainedMessages, parts: view.parts.filter((p) => retainedIds.has(p.messageId)),
    } });
  }, [view, maxMessages, trimTo, protectedMessageId]);

  // Register the mirror cleanup before the connection cleanup, which saves
  // any not-yet-rendered events on top of this final rendered snapshot.
  useSessionCache(sessionId, view, totalMessages, pageSize);
  const lifecycle = useSessionLifecycle({
    sessionId, routedPlatform, fetchSession, reconnectDelay, pageSize, debug,
    dispatch, viewRef, loadMoreAbortRef, loadMoreInFlightRef, setLoadingMore, setTotalMessages,
  });

  const { id: liveId, platform, status: liveStatus, notice, liveConnection } = view.session ?? {};
  useEffect(() => {
    // Keep the shared row current when the detail stream leads global events.
    if (!liveId || liveId !== sessionId || view._statusFresh === false) return;
    patchSessionLists(liveId, {
      status: liveStatus, notice, liveConnection,
    }, platform);
  }, [sessionId, liveId, platform, liveStatus, notice, liveConnection,
    view._statusFresh, view.session]);

  const loadMore = useCallback(async () => {
    // The ref makes two same-tick calls re-entrant-safe.
    if (!sessionId || loadMoreInFlightRef.current) return;
    loadMoreInFlightRef.current = true;
    setLoadingMore(true);
    const offset = platformMessageCount(viewRef.current.messages);
    const controller = new AbortController();
    loadMoreAbortRef.current = controller;
    try {
      const detail = await fetchSession(sessionId, pageSize, offset, controller.signal, routedPlatform, true);
      if (controller.signal.aborted) return;
      // SSE or navigation may have changed the view while this request waited.
      const current = viewRef.current;
      if (current.sessionId !== sessionId || detail.session.id !== sessionId) return;
      const newMsgs = detail.messages || [];
      const newParts = detail.parts || [];
      if (newMsgs.length === 0) return;
      const existingIds = new Set(current.messages.map((m) => m.id));
      const existingPartIds = new Set(current.parts.map((p) => p.id));
      dispatch({ type: 'load', view: {
        ...current,
        messages: [...newMsgs.filter((m) => !existingIds.has(m.id)), ...current.messages],
        parts: [...newParts.filter((p) => !existingPartIds.has(p.id)), ...current.parts],
      } });
      setTotalMessages((prev) => Math.max(prev, detail.totalMessages || detail.session.messageCount || 0));
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      remoteLog.error('loadMore failed', err);
    } finally {
      if (loadMoreAbortRef.current === controller) loadMoreAbortRef.current = null;
      loadMoreInFlightRef.current = false;
      setLoadingMore(false);
    }
  }, [sessionId, fetchSession, pageSize, routedPlatform, setTotalMessages]);

  const hydrateHistory = useCallback((messages: Message[], parts: Part[]) => {
    const current = viewRef.current;
    const currentMessages = new Map(current.messages.map((message) => [message.id, message]));
    const currentParts = new Map(current.parts.map((part) => [part.id, part]));
    const fetchedMessageIDs = new Set(messages.map((message) => message.id));
    const fetchedPartIDs = new Set(parts.map((part) => part.id));
    dispatch({ type: 'load', view: {
      ...current,
      messages: [...messages.map((message) => currentMessages.get(message.id) ?? message),
        ...current.messages.filter((message) => !fetchedMessageIDs.has(message.id))],
      parts: [...parts.map((part) => currentParts.get(part.id) ?? part),
        ...current.parts.filter((part) => !fetchedPartIDs.has(part.id))],
    } });
    setTotalMessages((total) => Math.max(total, messages.length));
  }, [setTotalMessages]);

  return { ...view, ...lifecycle, loadMore, hydrateHistory, loadingMore, totalMessages,
    clearPrompt, setPendingPermission, setPendingQuestion, patchSession, dispatch };
}
