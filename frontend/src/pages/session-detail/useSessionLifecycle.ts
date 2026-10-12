import { useCallback, useEffect, useRef, useState, type Dispatch, type MutableRefObject } from 'react';
import { useApiStore } from '../../lib/apiStore';
import { initialSessionView, seedDeltaOwnedFields, type SessionView } from '../../lib/sessionReducer';
import { onPageResume } from '../../lib/pageResume';
import { createSessionSse, reduceBatchedSessionView } from './sessionSse';
import { isSessionPageRestore, latestPage, viewFromDetail } from './sessionView';
import type { SseDebugEvent, UseSessionOptions, UseSessionStatus } from './useSession';

interface Options {
  sessionId?: string;
  routedPlatform?: string;
  fetchSession: NonNullable<UseSessionOptions['fetchSession']>;
  reconnectDelay: (attempt: number) => number;
  pageSize: number;
  debug: boolean;
  dispatch: Dispatch<Parameters<typeof reduceBatchedSessionView>[1]>;
  viewRef: MutableRefObject<SessionView>;
  loadMoreAbortRef: MutableRefObject<AbortController | null>;
  loadMoreInFlightRef: MutableRefObject<boolean>;
  setLoadingMore: (loading: boolean) => void;
  setTotalMessages: Dispatch<import('react').SetStateAction<number>>;
}

/** Owns the initial load, reconciliation, and the session event connection. */
export function useSessionLifecycle({ sessionId, routedPlatform, fetchSession, reconnectDelay, pageSize, debug,
  dispatch, viewRef, loadMoreAbortRef, loadMoreInFlightRef, setLoadingMore, setTotalMessages }: Options) {
  const cached = sessionId ? useApiStore.getState().getCachedSession(sessionId) : null;
  const [status, setStatus] = useState<UseSessionStatus>(cached ? 'live' : 'loading');
  const [loading, setLoading] = useState(!cached);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(!!cached);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [sseReconnecting, setSseReconnecting] = useState(false);
  const [sseReconnectAttempt, setSseReconnectAttempt] = useState(0);
  const [sseNextRetryAt, setSseNextRetryAt] = useState<number | null>(null);
  const [sseDebugEvents, setSseDebugEvents] = useState<SseDebugEvent[]>([]);
  const [changesDirtyTick, setChangesDirtyTick] = useState(0);
  const abortRef = useRef<AbortController | null>(null);
  const reloadRef = useRef<() => Promise<void>>(async () => {});
  const refreshRef = useRef<() => Promise<void>>(async () => {});
  const retryNowRef = useRef<() => void>(() => {});
  const entryRef = useRef({ id: sessionId, opened: isSessionPageRestore(sessionId) });
  const setCachedSession = useApiStore((s) => s.setCachedSession);
  const updateCachedSession = useApiStore((s) => s.updateCachedSession);
  const reload = useCallback(async () => reloadRef.current(), []);
  const refresh = useCallback(async () => refreshRef.current(), []);
  const retryNow = useCallback(() => retryNowRef.current(), []);

  useEffect(() => {
    if (entryRef.current.id !== sessionId) entryRef.current = { id: sessionId, opened: false };
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
    const nextCached = useApiStore.getState().getCachedSession(sessionId);
    const nextInitial: SessionView = nextCached
      ? { ...viewFromDetail(sessionId, nextCached), _deltaOwnedFields: seedDeltaOwnedFields(nextCached.parts) }
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
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let attempt = 0;
    let hasConnectedOnce = false;
    let reconnectingAfterError = false;
    const hasContent = () => viewRef.current.sessionId === sessionId && viewRef.current.session !== null;
    // Reconcile by default: SSE leads the database, so replacement can lose text.
    const doFetch = async (mode: 'replace' | 'reconcile' = 'reconcile') => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      try {
        const detail = await fetchSession(sessionId, pageSize, 0, controller.signal, routedPlatform, opened);
        if (cancelled || controller.signal.aborted) return false;
        opened = true;
        entryRef.current.opened = true;
        events.flush();
        dispatch({ type: 'load', view: viewFromDetail(sessionId, detail), mode });
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
        setStatus((prev) => prev === 'loading' ? 'live' : prev);
        setLoading(false);
        return true;
      } catch (err) {
        if (cancelled || controller.signal.aborted) return false;
        if (err instanceof DOMException && err.name === 'AbortError') return false;
        const message = err instanceof Error ? err.message : 'Failed to load session';
        setRefreshing(false);
        setLoading(false);
        if (hasContent()) { setRefreshError(message); return false; }
        setLoadError(message);
        setStatus('error');
        return false;
      }
    };
    const events = createSessionSse({
      sessionId, dispatch,
      reconcile: () => doFetch('reconcile'),
      dirty: () => setChangesDirtyTick((t) => t + 1),
      debug: debug ? (rows) => setSseDebugEvents((prev) => [...prev, ...rows].slice(-50)) : undefined,
    });
    reloadRef.current = async () => { if (hasContent()) setRefreshing(true); await doFetch('replace'); };
    refreshRef.current = async () => { if (hasContent()) setRefreshing(true); await doFetch('reconcile'); };
    const connect = () => {
      if (cancelled) return;
      const query = routedPlatform ? `?platform=${encodeURIComponent(routedPlatform)}` : '';
      evtSource = new EventSource(`/api/session/${encodeURIComponent(sessionId)}/events${query}`);
      evtSource.onopen = () => {
        if (cancelled) return;
        attempt = 0;
        setStatus('live');
        setSseReconnecting(false);
        setSseReconnectAttempt(0);
        setSseNextRetryAt(null);
        if (hasConnectedOnce || reconnectingAfterError) {
          void doFetch('reconcile');
          setChangesDirtyTick((t) => t + 1);
        }
        hasConnectedOnce = true;
        reconnectingAfterError = false;
      };
      events.attach(evtSource);
      evtSource.onerror = () => {
        if (cancelled) return;
        events.flush();
        reconnectingAfterError = true;
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
    void doFetch();
    connect();
    const unsubscribeResume = onPageResume(() => retryNowRef.current());
    return () => {
      cancelled = true;
      unsubscribeResume();
      const pending = events.dispose();
      // Cache pending events without dispatching them into the next session.
      // This is a state snapshot ref, not a DOM node: cleanup needs its latest view.
      if (pending.length && viewRef.current.sessionId === sessionId) {
        // eslint-disable-next-line react-hooks/exhaustive-deps
        const outgoing = reduceBatchedSessionView(viewRef.current, { type: 'sseBatch', events: pending });
        updateCachedSession(sessionId, (prev) => ({ ...prev, ...latestPage(outgoing.messages, outgoing.parts, pageSize) }));
      }
      abortRef.current?.abort();
      abortRef.current = null;
      loadMoreAbortRef.current?.abort();
      loadMoreAbortRef.current = null;
      // Injected fetchers may ignore abort. Abandon their pagination slot now.
      loadMoreInFlightRef.current = false;
      setLoadingMore(false);
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
