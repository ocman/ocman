import { useCallback, useEffect, useRef, useState, type Dispatch, type MutableRefObject } from 'react';
import { useApiStore } from '../../lib/apiStore';
import { APIError, AuthError } from '../../lib/api.requests';
import { initialSessionView, seedDeltaOwnedFields, type SessionView } from '../../lib/sessionReducer';
import { onPageResume } from '../../lib/pageResume';
import { watchKeepalive } from '../../lib/sseKeepalive';
import { conversationStreamUrl, createConversationStream, type StreamDebugEvent } from '../../lib/conversation/conversationStream';
import { reduceConversation } from '../../lib/conversation/conversationReducer';
import { eventsAfter, parseCursor, type OceEnvelope } from '../../lib/conversation/events';
import type { LiveSessionAction } from '../../lib/conversation/liveSessionView';
import { notifyQueueUpdated } from '../../lib/useGlobalEvents';
import { isSessionPageRestore, latestPage, viewFromDetail } from './sessionView';
import type { UseSessionOptions, UseSessionStatus } from './useSession';

/** Coalesces history commits that land together into one read. */
export const COMMIT_READ_DELAY_MS = 250;

interface Options {
  sessionId?: string;
  routedPlatform?: string;
  fetchSession: NonNullable<UseSessionOptions['fetchSession']>;
  reconnectDelay: (attempt: number) => number;
  pageSize: number;
  debug: boolean;
  dispatch: Dispatch<LiveSessionAction>;
  viewRef: MutableRefObject<SessionView>;
  loadMoreAbortRef: MutableRefObject<AbortController | null>;
  loadMoreInFlightRef: MutableRefObject<boolean>;
  setLoadingMore: (loading: boolean) => void;
  setTotalMessages: Dispatch<import('react').SetStateAction<number>>;
}

/** Owns the initial load, reconciliation, and the session event connection. */
export function useSessionLifecycle({ sessionId, routedPlatform, fetchSession, reconnectDelay, pageSize, debug,
  dispatch, viewRef, loadMoreAbortRef, loadMoreInFlightRef, setLoadingMore, setTotalMessages }: Options) {
  const candidate = sessionId ? useApiStore.getState().getCachedSession(sessionId) : null;
  const cached = candidate && (!routedPlatform || candidate.session.platform === routedPlatform) ? candidate : null;
  const [status, setStatus] = useState<UseSessionStatus>(cached ? 'live' : 'loading');
  const [loading, setLoading] = useState(!cached);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(!!cached);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [sseReconnecting, setSseReconnecting] = useState(false);
  const [sseReconnectAttempt, setSseReconnectAttempt] = useState(0);
  const [sseNextRetryAt, setSseNextRetryAt] = useState<number | null>(null);
  const [sseDebugEvents, setSseDebugEvents] = useState<StreamDebugEvent[]>([]);
  const [changesDirtyTick, setChangesDirtyTick] = useState(0);
  const abortRef = useRef<AbortController | null>(null);
  const reloadRef = useRef<() => Promise<void>>(async () => {});
  const refreshRef = useRef<() => Promise<void>>(async () => {});
  const retryNowRef = useRef<() => void>(() => {});
  const entryRef = useRef({ id: sessionId, platform: routedPlatform, opened: isSessionPageRestore(sessionId) });
  const setCachedSession = useApiStore((s) => s.setCachedSession);
  const updateCachedSession = useApiStore((s) => s.updateCachedSession);
  const reload = useCallback(async () => reloadRef.current(), []);
  const refresh = useCallback(async () => refreshRef.current(), []);
  const retryNow = useCallback(() => retryNowRef.current(), []);

  useEffect(() => {
    if (entryRef.current.id !== sessionId || entryRef.current.platform !== routedPlatform) entryRef.current = { id: sessionId, platform: routedPlatform, opened: false };
    // eslint-disable-next-line react-hooks/set-state-in-effect -- resets view state on session switch in the same effect that owns the SSE connection; deriving it in render would need a full lifecycle rewrite
    if (!sessionId) { setStatus('loading'); return; }
    if (sessionId === 'new') {
      dispatch({ type: 'load', view: initialSessionView(sessionId) });
      setStatus('live');
      setLoading(false);
      setLoadError(null);
      setRefreshing(false);
      setRefreshError(null);
      setTotalMessages(0);
      return;
    }
    // Reset immediately to the target, preserving delta ownership in cached text.
    const nextCandidate = useApiStore.getState().getCachedSession(sessionId);
    const nextCached = nextCandidate && (!routedPlatform || nextCandidate.session.platform === routedPlatform) ? nextCandidate : null;
    const nextInitial: SessionView = nextCached
      ? { ...viewFromDetail(sessionId, nextCached), _statusFresh: false, _deltaOwnedFields: seedDeltaOwnedFields(nextCached.parts) }
      : initialSessionView(sessionId);
    dispatch({ type: 'load', view: nextInitial });
    setStatus(nextCached ? 'live' : 'loading');
    setLoading(!nextCached);
    setLoadError(null);
    setRefreshing(!!nextCached);
    setRefreshError(null);
    setTotalMessages(nextCached?.totalMessages || nextCached?.session.messageCount || 0);

    let cancelled = false;
    // Page restores and subsequent reads cannot undo another client's archive.
    let opened = entryRef.current.opened;
    let evtSource: EventSource | null = null;
    let stopWatch = () => {};
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let fetchRetryTimer: ReturnType<typeof setTimeout> | undefined;
    let fetchRetryAttempt = 0;
    let attempt = 0;
    let recoveryEvents: OceEnvelope[] | null = null;
    // Events received while the latest read is in flight.
    let readEvents: OceEnvelope[] | null = null;
    let commitTimer: ReturnType<typeof setTimeout> | undefined;
    let recoveryMode: 'recover' | 'restore' = 'recover';
    let upstreamFailed = false;
    let readBlocked = false;
    const hasContent = () => viewRef.current.sessionId === sessionId && viewRef.current.session !== null;
    // Reconcile by default: SSE leads the database, so replacement can lose text.
    const doFetch = async (requestedMode: 'replace' | 'reconcile' | 'recover' | 'restore' = 'reconcile') => {
      clearTimeout(fetchRetryTimer);
      fetchRetryTimer = undefined;
      events.flush();
      // A routine refresh must not cancel an authoritative recovery and
      // replace it with union merging (e.g. a child arriving during recovery).
      const mode = recoveryEvents && requestedMode === 'reconcile' ? recoveryMode : requestedMode;
      // A reconcile that supersedes recovery must keep its original replay
      // fence. The newer REST read can still lag the final streamed response,
      // and idle/turn-end events have already cleared the live-turn ledger.
      const sinceFence: OceEnvelope[] = recoveryEvents && requestedMode === 'reconcile' ? [...recoveryEvents] : [];
      recoveryEvents = mode === 'recover' || mode === 'restore' ? sinceFence : null;
      if (mode === 'recover' || mode === 'restore') recoveryMode = mode;
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      const fence: OceEnvelope[] = [];
      readEvents = fence;
      // Live status seen after this point outranks the response's.
      const statusRevision = viewRef.current._statusRevision ?? 0;
      try {
        const detail = await fetchSession(sessionId, pageSize, 0, controller.signal, routedPlatform, opened);
        if (cancelled || controller.signal.aborted) return false;
        opened = true;
        entryRef.current.opened = true;
        events.flush();
        if (cancelled || controller.signal.aborted) return false;
        // The owner composes streamed content history lacks up to the cursor;
        // only later events are newer than the read.
        const cursor = detail.stream?.cursor;
        if (mode === 'recover' || mode === 'restore') {
          loadMoreAbortRef.current?.abort();
          loadMoreInFlightRef.current = false;
          setLoadingMore(false);
          dispatch({ type: 'recover', view: viewFromDetail(sessionId, detail), sinceFence: eventsAfter(sinceFence, cursor),
            restoreLiveFields: mode === 'restore', covered: !!cursor && !!parseCursor(cursor) });
        } else dispatch({ type: 'load', view: viewFromDetail(sessionId, detail), mode, statusRevision, sinceFence: eventsAfter(fence, cursor) });
        setTotalMessages(detail.totalMessages || detail.session.messageCount || 0);
        setLoadError(null);
        setRefreshing(false);
        setRefreshError(null);
        setCachedSession(sessionId, {
          session: { ...detail.session, contextTokenCount: detail.session.contextTokenCount ?? detail.contextTokenCount },
          sessionTree: detail.sessionTree,
          messages: detail.messages,
          parts: detail.parts,
          totalMessages: detail.totalMessages || detail.session.messageCount || 0,
          contextTokenCount: detail.contextTokenCount,
          defaultAgent: detail.defaultAgent,
          defaultModel: detail.defaultModel,
          warnings: detail.warnings,
        });
        setStatus((prev) => prev === 'loading' || prev === 'error' ? 'live' : prev);
        setLoading(false);
        fetchRetryAttempt = 0;
        readBlocked = false;
        if (recoveryEvents === sinceFence) recoveryEvents = null;
        return true;
      } catch (err) {
        if (cancelled || controller.signal.aborted) return false;
        if (err instanceof DOMException && err.name === 'AbortError') return false;
        const message = err instanceof Error ? err.message : 'Failed to load session';
        // A live HTTP stream can stay healthy after a REST repair fails. Do
        // not depend on another lifecycle edge to heal the missing content.
        readBlocked = err instanceof AuthError || (err instanceof APIError && err.status < 500 && err.status !== 408 && err.status !== 429);
        if (!readBlocked) {
          const delay = Math.min(1_000 * 2 ** Math.min(fetchRetryAttempt++, 4), 15_000);
          fetchRetryTimer = setTimeout(() => { void doFetch(mode === 'replace' ? 'replace' : 'reconcile'); }, delay);
        }
        setRefreshing(false);
        setLoading(false);
        if (hasContent()) { setRefreshError(message); return false; }
        setLoadError(message);
        setStatus('error');
        return false;
      } finally {
        if (readEvents === fence) readEvents = null;
      }
    };
    const events = createConversationStream({
      sessionId, dispatch: (action) => {
        recoveryEvents?.push(...action.envelopes);
        readEvents?.push(...action.envelopes);
        dispatch(action);
      },
      reconcile: () => doFetch('reconcile'),
      recover: () => doFetch('recover'),
      upstream: (error) => {
        if (cancelled) return;
        upstreamFailed = !!error;
        setStatus(error ? 'reconnecting' : 'live');
        setSseReconnecting(!!error);
        setRefreshError(error);
      },
      dirty: () => setChangesDirtyTick((t) => t + 1),
      // History stored what it trailed. A settled conversation rereads it to
      // fill anything the stream missed; a running turn reconciles at its end.
      committed: () => {
        clearTimeout(commitTimer);
        commitTimer = setTimeout(() => {
          if (viewRef.current.sessionId === sessionId && viewRef.current.session?.status === 'busy') return;
          void doFetch('recover');
        }, COMMIT_READ_DELAY_MS);
      },
      queue: (messages) => notifyQueueUpdated(sessionId, messages),
      hasStale: () => viewRef.current.sessionId === sessionId && !!viewRef.current._stale?.size,
      isBusy: () => !readBlocked && viewRef.current.sessionId === sessionId && viewRef.current.session?.status === 'busy',
      debug: debug ? (rows) => setSseDebugEvents((prev) => [...prev, ...rows].slice(-50)) : undefined,
    });
    reloadRef.current = async () => { if (hasContent()) setRefreshing(true); await doFetch('replace'); };
    refreshRef.current = async () => { if (hasContent()) setRefreshing(true); await doFetch('reconcile'); };
    const connect = () => {
      if (cancelled) return;
      stopWatch();
      // Resume from the last folded envelope; the server decides whether it can.
      evtSource = new EventSource(conversationStreamUrl(sessionId, routedPlatform, events.cursor()));
      stopWatch = watchKeepalive(evtSource, () => {
        // Even a stream that never opened may have missed events.
        retryNowRef.current();
      });
      evtSource.onopen = () => {
        if (cancelled) return;
        attempt = 0;
        setStatus(upstreamFailed ? 'reconnecting' : 'live');
        setSseReconnecting(upstreamFailed);
        setSseReconnectAttempt(0);
        setSseNextRetryAt(null);
      };
      // Reconciling waits for the hello: a resumed stream needs no refetch.
      events.attach(evtSource);
      evtSource.onerror = () => {
        if (cancelled) return;
        events.flush();
        stopWatch();
        evtSource?.close();
        evtSource = null;
        const delay = reconnectDelay(attempt);
        attempt += 1;
        setStatus('reconnecting');
        setSseReconnecting(true);
        setSseReconnectAttempt(attempt);
        setSseNextRetryAt(Date.now() + delay);
        reconnectTimer = setTimeout(connect, delay);
      };
    };
    retryNowRef.current = () => {
      if (cancelled) return;
      if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
      evtSource?.close();
      evtSource = null;
      setSseNextRetryAt(null);
      connect();
    };
    // A cached page may have missed removals while unmounted. Rebuild it
    // authoritatively, preserving only stream events observed during the read.
    void doFetch('restore');
    connect();
    const unsubscribeResume = onPageResume(() => retryNowRef.current());
    return () => {
      cancelled = true;
      clearTimeout(fetchRetryTimer);
      clearTimeout(commitTimer);
      unsubscribeResume();
      const pending = events.dispose();
      // Cache pending events without dispatching them into the next session.
      // This is a state snapshot ref, not a DOM node: cleanup needs its latest view.
      if (pending.length && viewRef.current.sessionId === sessionId) {
        // eslint-disable-next-line react-hooks/exhaustive-deps
        const outgoing = reduceConversation(viewRef.current, pending);
        updateCachedSession(sessionId, (prev) => ({ ...prev, ...latestPage(outgoing.messages, outgoing.parts, pageSize) }));
      }
      abortRef.current?.abort();
      abortRef.current = null;
      loadMoreAbortRef.current?.abort();
      loadMoreAbortRef.current = null;
      // Injected fetchers may ignore abort. Abandon their pagination slot now.
      loadMoreInFlightRef.current = false;
      setLoadingMore(false);
      stopWatch();
      evtSource?.close();
      evtSource = null;
      if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
      retryNowRef.current = () => {};
      setSseReconnecting(false);
      setSseReconnectAttempt(0);
      setSseNextRetryAt(null);
      setSseDebugEvents([]);
    };
    // Options are captured for the session. Owner changes restart the stream.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, routedPlatform]);

  return { status, loading, loadError, refreshing, refreshError,
    sseReconnecting, sseReconnectAttempt, sseNextRetryAt, sseDebugEvents, changesDirtyTick, reload, refresh, retryNow };
}
