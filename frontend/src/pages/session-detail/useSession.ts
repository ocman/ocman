// React hook that wraps the pure `sessionReducer` with the
// EventSource lifecycle described in spec/sse-rewrite/architecture.md.
//
// One reducer, one effect keyed on session id. On mount the hook
// fetches `/api/session/{id}`, opens `/api/session/{id}/events`, and
// batches SSE messages through the reducer. On error it
// closes the source and schedules a reconnect via the existing
// `sseBackoff` schedule; on successful reconnect it refetches so
// the gap is healed in one shot.
//
// All side effects that don't belong in a pure reducer live here:
// - the EventSource lifecycle and reconnect bookkeeping,
// - the refetch triggered by `session.idle`,
// - the AbortController used to cancel an in-flight fetch on
//   session change / unmount,
// - the cache seed/mirror via useApiStore.getCachedSession /
//   updateCachedSession (so revisits render instantly),
// - the debug-events ring (limited to the last 50 events; only
//   populated when the caller opts in),
// - the loadMore pagination shim against the same reducer.

import React, { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { api, type Message, type Part, type SessionDetail } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import {
  initialSessionView,
  seedDeltaOwnedFields,
  type SessionView,
} from '../../lib/sessionReducer';
import { computeReconnectDelay } from './sseBackoff';
import { createSessionSse, reduceBatchedSessionView } from './sessionSse';
import { remoteLog } from '../../lib/remoteLog';
import { useActivityScope } from '../../lib/activityScopes';

/** Live-pipeline status surfaced to the page. */
export type UseSessionStatus = 'loading' | 'live' | 'reconnecting' | 'error';

/** Single SSE event row in the debug overlay. */
export interface SseDebugEvent {
  at: number;
  event: string;
  data: string;
}

export interface UseSessionOptions {
  /**
   * Replaces the default `api.session` call. Tests use this to
   * inject fixtures without touching the network. Production code
   * leaves it undefined.
   */
  fetchSession?: (id: string, limit: number, offset: number, signal?: AbortSignal, platform?: string) => Promise<SessionDetail>;
  /**
   * Replaces the default exponential-backoff schedule. Tests use
   * this to drive reconnects without sleeping for 500 ms.
   */
  reconnectDelay?: (attempt: number) => number;
  /**
   * When true, captures the last 50 raw SSE events for the debug
   * overlay. Off by default — the page sets it from `?debug`.
   */
  debug?: boolean;
  /**
   * Page size for initial load + loadMore pagination. Defaults to 30,
   * matching the legacy useSessionMessages.
   */
  pageSize?: number;
  /**
   * Maximum messages retained in memory. When the count exceeds
   * this, the head is trimmed during render. 0 = no trim.
   */
  maxMessages?: number;
  /**
   * Floor target after trimming kicks in. Must be <= maxMessages.
   */
  trimTo?: number;
  /** Message that must stay in memory while a jump target is being loaded. */
  protectedMessageId?: string | null;
}

export interface UseSessionResult extends SessionView {
  status: UseSessionStatus;
  /** Force-refetch /api/session/{id} and replace state. */
  reload: () => Promise<void>;
  /** Prepend an older page. Idempotent across overlapping ids. */
  loadMore: () => Promise<void>;
  /** Merge fetched history into the live message set for an explicit jump. */
  hydrateHistory: (messages: Message[], parts: Part[]) => void;
  /** True for first load until the initial fetch resolves. */
  loading: boolean;
  /** True while a loadMore is in flight. */
  loadingMore: boolean;
  /** Error message from the most recent failed fetch, or null. */
  loadError: string | null;
  /** Total messages on the server (from the most recent load). */
  totalMessages: number;
  /** True between `onerror` and the next successful reconnect. */
  sseReconnecting: boolean;
  /** Consecutive reconnect attempts since the last successful open. */
  sseReconnectAttempt: number;
  /** Epoch-ms timestamp of the next scheduled reconnect, or null. */
  sseNextRetryAt: number | null;
  /** Cancel the backoff timer and reconnect immediately. */
  retryNow: () => void;
  /** Last 50 SSE events (when `debug: true`). */
  sseDebugEvents: SseDebugEvent[];
  /**
   * Tick that bumps when an edit/write tool part lands. Wired to the
   * right-panel changes/info refresh.
   */
  changesDirtyTick: number;
  /** Clear a pending permission/question prompt by id. No-ops if
   *  the id doesn't match the currently-displayed prompt. Routes
   *  through the reducer's clearPrompt action. */
  clearPrompt: (kind: 'permission' | 'question', id: string) => void;
  /** Imperatively set a pending permission. Used by the sidebar→
   *  detail reverse sync when poll discovers a prompt SSE missed.
   *  Set-only by design — clearing goes through the id-safe
   *  `clearPrompt`, so the type forbids passing null. */
  setPendingPermission: (
    perm: import('../../lib/sseHelpers').PendingPermission,
    ownerIds?: string[],
  ) => void;
  /** Imperatively set a pending question. Same rationale as
   *  setPendingPermission, including the set-only contract. */
  setPendingQuestion: (q: import('../../components/session/QuestionPrompt').PendingQuestion) => void;
  /** Apply a partial patch to the session metadata. Used for
   *  page-local self-mutations like rename / mark-seen. */
  patchSession: (patch: Partial<import('../../lib/sessionReducer').SessionMetadata>) => void;
  /** Raw reducer dispatch. Exposed so callers can inject synthetic
   *  actions (e.g. auto-approve notices) without coupling to internal
   *  reducer helpers. */
  dispatch: React.Dispatch<import('../../lib/sessionReducer').SessionAction>;
}

const DEFAULT_PAGE_SIZE = 30;
/** Trailing debounce for the session-cache mirror (#460). */
const CACHE_MIRROR_DEBOUNCE_MS = 500;

/**
 * Default fetcher — wraps `api.session`. Pagination shape mirrors
 * what useApiStore.getSession exposed; we hit the raw module helper
 * directly so the hook doesn't depend on the store's selector.
 */
async function defaultFetchSession(
  id: string,
  limit: number,
  offset: number,
  signal?: AbortSignal,
  platform?: string,
): Promise<SessionDetail> {
  return api.session(id, limit, offset, signal, platform);
}

/** Build a SessionView from a freshly-fetched SessionDetail. */
function viewFromDetail(id: string, detail: SessionDetail): SessionView {
  let messages = detail.messages ?? [];
  let parts = detail.parts ?? [];
  const notice = detail.session.notice;
  if (notice) {
    const noticeID = `ocman-session-notice-${id}`;
    const timeCreated = detail.session.timeUpdated || Date.now();
    const noticeMsg: Message = {
      id: noticeID,
      sessionId: id,
      timeCreated,
      data: { role: 'notice' },
    };
    const noticePart: Part = {
      id: `${noticeID}-part`,
      messageId: noticeID,
      sessionId: id,
      timeCreated,
      data: { type: 'text', text: notice.message },
    };
    messages = [...messages.filter((m) => m.id !== noticeID), noticeMsg];
    parts = [...parts.filter((p) => p.messageId !== noticeID), noticePart];
  }
  return {
    ...initialSessionView(id),
    session: {
      ...detail.session,
      contextTokenCount: detail.session.contextTokenCount ?? detail.contextTokenCount,
      defaultAgent: detail.defaultAgent,
      defaultModel: detail.defaultModel,
      warnings: detail.warnings ?? [],
    },
    sessionTree: detail.sessionTree ?? [],
    messages,
    parts,
  };
}

/**
 * The hook. Returns a `SessionView` plus a `status` and `reload()`.
 * The state has the reducer's internal `_deltaOwnedFields` /
 * `_refetchRequested` fields too — they're harmless for consumers
 * (prefixed `_`) and let us avoid a wrapper allocation per render.
 */
export function useSession(
  sessionId: string | undefined,
  options: UseSessionOptions = {},
): UseSessionResult {
  useActivityScope(sessionId && sessionId !== 'new' ? `session:${sessionId}` : undefined);
  const fetchSession = options.fetchSession ?? defaultFetchSession;
  const reconnectDelay = options.reconnectDelay ?? computeReconnectDelay;
  const pageSize = options.pageSize ?? DEFAULT_PAGE_SIZE;
  const maxMessages = options.maxMessages ?? 0;
  const trimTo = options.trimTo ?? maxMessages;
  const protectedMessageId = options.protectedMessageId ?? null;
  const debug = options.debug ?? false;

  // Read cache once at mount via getState() — we want a snapshot,
  // not a reactive subscription. Subscribing here would cause every
  // cache write (mirror effect) to re-run this hook's setup.
  const cached = sessionId ? useApiStore.getState().getCachedSession(sessionId) : null;
  const cachedPlatform = cached?.session.platform;
  const routedPlatform = cachedPlatform?.startsWith('r-') ? cachedPlatform : undefined;
  const initialView: SessionView = cached
    ? {
        ...initialSessionView(sessionId!),
        session: {
          ...cached.session,
          contextTokenCount: cached.session.contextTokenCount ?? cached.contextTokenCount,
          defaultAgent: cached.defaultAgent,
          defaultModel: cached.defaultModel,
          warnings: cached.warnings ?? [],
        },
        sessionTree: cached.sessionTree ?? [],
        messages: cached.messages,
        parts: cached.parts,
      }
    : initialSessionView(sessionId ?? '');

  const [view, dispatch] = useReducer(reduceBatchedSessionView, initialView);
  const [status, setStatus] = useState<UseSessionStatus>(cached ? 'live' : 'loading');
  const [loading, setLoading] = useState<boolean>(!cached);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [totalMessages, setTotalMessages] = useState<number>(
    cached?.totalMessages || cached?.session.messageCount || 0,
  );
  const [sseReconnecting, setSseReconnecting] = useState(false);
  const [sseReconnectAttempt, setSseReconnectAttempt] = useState(0);
  const [sseNextRetryAt, setSseNextRetryAt] = useState<number | null>(null);
  const [sseDebugEvents, setSseDebugEvents] = useState<SseDebugEvent[]>([]);
  const [changesDirtyTick, setChangesDirtyTick] = useState(0);

  // Refs hold non-reactive pieces. retryNowRef / reloadRef expose
  // imperative handles across the effect-closure boundary without
  // forcing the effect to re-run.
  const abortRef = useRef<AbortController | null>(null);
  // Pagination has its own controller so the session effect's cleanup
  // can cancel an in-flight older-page fetch on navigation, plus an
  // in-flight flag that (unlike the `loadingMore` state) is current
  // within the same tick.
  const loadMoreAbortRef = useRef<AbortController | null>(null);
  const loadMoreInFlightRef = useRef(false);
  const reloadRef = useRef<() => Promise<void>>(async () => {});
  const retryNowRef = useRef<() => void>(() => {});
  // Snapshot of the latest view, kept current via render-phase
  // assignment. `loadMore` reads from it without taking a dep.
  const viewRef = useRef(view);
  viewRef.current = view;

  const reload = useCallback(async () => reloadRef.current(), []);
  const retryNow = useCallback(() => retryNowRef.current(), []);
  const clearPrompt = useCallback(
    (kind: 'permission' | 'question', id: string) => {
      dispatch({ type: 'clearPrompt', kind, id });
    },
    [],
  );
  const setPendingPermission = useCallback(
    (perm: import('../../lib/sseHelpers').PendingPermission, ownerIds?: string[]) => {
      dispatch({ type: 'setPendingPermission', permission: perm, ownerIds });
    },
    [],
  );
  const setPendingQuestion = useCallback(
    (q: import('../../components/session/QuestionPrompt').PendingQuestion) => {
      dispatch({
        type: 'sse',
        event: {
          type: 'question.asked',
          properties: {
            id: q.requestId,
            sessionID: q.sessionID,
            questions: q.questions,
          },
        },
      });
    },
    [],
  );
  const patchSession = useCallback(
    (patch: Partial<import('../../lib/sessionReducer').SessionMetadata>) => {
      dispatch({ type: 'patchSession', patch });
    },
    [],
  );

  const setCachedSession = useApiStore((s) => s.setCachedSession);
  const updateCachedSession = useApiStore((s) => s.updateCachedSession);

  // Memory trimming runs in render against the view's messages.
  // We mutate state via the reducer (a "trim" via `load` action
  // would discard delta ownership, which is wrong) — but the
  // architecture says: "memory trimming runs against the reducer
  // output, not as a separate setMessages call". We achieve that
  // by dispatching a synthesised load with the trimmed slice when
  // overflow is detected. The dispatch is keyed on the messages
  // length so it doesn't re-fire when nothing changed.
  const lastTrimmedLengthRef = useRef<number>(0);
  useEffect(() => {
    if (maxMessages <= 0) return;
    if (view.messages.length <= maxMessages) return;
    if (lastTrimmedLengthRef.current === view.messages.length) return;
    const protectedIndex = protectedMessageId
      ? view.messages.findIndex((m) => m.id === protectedMessageId)
      : -1;
    if (protectedMessageId && protectedIndex === -1) return;
    lastTrimmedLengthRef.current = view.messages.length;
    const retained = view.messages.slice(-trimTo);
    const retainedMessages = protectedIndex >= 0 && !retained.some((m) => m.id === protectedMessageId)
      ? [view.messages[protectedIndex], ...retained.slice(1)]
      : retained;
    const retainedIds = new Set(retainedMessages.map((m) => m.id));
    const retainedParts = view.parts.filter((p) => retainedIds.has(p.messageId));
    dispatch({
      type: 'load',
      view: {
        ...view,
        messages: retainedMessages,
        parts: retainedParts,
      },
    });
  }, [view, maxMessages, trimTo, protectedMessageId]);

  // Cache mirror — write the latest view into the per-session cache
  // so revisits render instantly. No-ops when the session isn't
  // cached. Only runs after the first successful load.
  //
  // Guard: only write when the reducer's sessionId matches the prop.
  // After a navigation the prop (sessionId) changes synchronously, but
  // the reducer state still reflects the old session until the dispatch
  // inside the main useEffect fires. Without this guard the cache mirror
  // would run with sessionId=B / view.messages=A_messages, corrupting
  // session B's cache entry with session A's content.
  //
  // Writes are debounced (#460): the deps change on every streaming
  // token delta, and each write clones the whole sessionCache Map and
  // notifies every store subscriber. The cache only exists to make a
  // revisit warm, so a trailing write per burst plus a flush on
  // unmount/session-switch gives the same behaviour at a tiny fraction
  // of the cost.
  const mirrorWriteRef = useRef<(() => void) | null>(null);
  const mirrorTimerRef = useRef<number | null>(null);
  useEffect(() => {
    if (!sessionId || !view.session) return;
    if (view.sessionId !== sessionId) return;
    const { defaultAgent, defaultModel, ...sessionForCache } = view.session;
    void defaultAgent;
    void defaultModel;
    mirrorWriteRef.current = () => {
      updateCachedSession(sessionId, (prev) => ({
        ...prev,
        session: sessionForCache,
        sessionTree: view.sessionTree,
        messages: view.messages,
        parts: view.parts,
        totalMessages: Math.max(prev.totalMessages ?? 0, totalMessages),
      }));
    };
    if (mirrorTimerRef.current === null) {
      mirrorTimerRef.current = window.setTimeout(() => {
        mirrorTimerRef.current = null;
        mirrorWriteRef.current?.();
      }, CACHE_MIRROR_DEBOUNCE_MS);
    }
  }, [sessionId, view.sessionId, view.session, view.sessionTree, view.messages, view.parts, totalMessages, updateCachedSession]);

  // Flush the pending mirror on unmount and on session switch, so the
  // cache is current when the user navigates away mid-stream. Cleanup
  // runs before the next session's effects, so the captured write still
  // belongs to the outgoing session.
  useEffect(() => () => {
    if (mirrorTimerRef.current !== null) {
      window.clearTimeout(mirrorTimerRef.current);
      mirrorTimerRef.current = null;
    }
    mirrorWriteRef.current?.();
    mirrorWriteRef.current = null;
  }, [sessionId]);

  useEffect(() => {
    if (!sessionId) {
      setStatus('loading');
      return;
    }

    // The sentinel `new` id means "no session open" — the last one was
    // archived and none remained. Render the empty-detail hint instead
    // of fetching /api/session/new (which 404s). The sidebar still
    // polls and populates on its own.
    if (sessionId === 'new') {
      dispatch({ type: 'load', view: initialSessionView(sessionId) });
      setStatus('live');
      setLoading(false);
      setLoadError(null);
      setTotalMessages(0);
      return;
    }

    // Immediately reset reducer state to the target session so the
    // header and thread update synchronously with the URL change —
    // before the REST fetch resolves. Without this, the previous
    // session's title/messages remain visible until doFetch() lands,
    // which can take 150–500 ms on a slow connection or under load.
    // Use the cache when available so revisits feel instant; fall
    // back to a blank view (session: null → loading spinner).
    //
    // When seeding from cache we also rebuild the reducer's
    // `_deltaOwnedFields` map from the cached parts. The cache stores
    // text/output that was accumulated from SSE deltas, but the
    // ownership map is reducer-internal and isn't persisted. Without
    // re-seeding it, a reconcile-mode load against a DB-lagging server
    // response would wipe chunks that the server hadn't recorded yet
    // — the "missing sections after switching sessions" regression.
    const nextCached = useApiStore.getState().getCachedSession(sessionId);
    const nextInitial: SessionView = nextCached
      ? {
          ...initialSessionView(sessionId),
          session: {
            ...nextCached.session,
            contextTokenCount: nextCached.session.contextTokenCount ?? nextCached.contextTokenCount,
            defaultAgent: nextCached.defaultAgent,
            defaultModel: nextCached.defaultModel,
          },
          sessionTree: nextCached.sessionTree ?? [],
          messages: nextCached.messages,
          parts: nextCached.parts,
          _deltaOwnedFields: seedDeltaOwnedFields(nextCached.parts),
        }
      : initialSessionView(sessionId);
    dispatch({ type: 'load', view: nextInitial });
    setStatus(nextCached ? 'live' : 'loading');
    setLoading(!nextCached);
    setLoadError(null);
    setTotalMessages(nextCached?.totalMessages || nextCached?.session.messageCount || 0);

    let cancelled = false;
    let evtSource: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let attempt = 0;
    let hasConnectedOnce = false;
    let reconnectingAfterError = false;

    /**
     * Fetch the session detail and dispatch a load. `mode` controls
     * whether the load replaces state wholesale or reconciles
     * (preserving in-memory data the server hasn't caught up with).
     *
     * Default is 'reconcile': during active streaming, OpenCode's
     * SSE stream leads its database by a few hundred ms, so a
     * wholesale replace would transiently wipe live content. Even
     * the initial mount can race with already-flowing SSE events,
     * so we default to the safe option.
     *
     * 'replace' is reserved for the user's explicit reload()
     * (clicking the retry banner): they want fresh authoritative
     * state, and there is no in-memory streamed content worth
     * preserving in that scenario.
     */
    const doFetch = async (mode: 'replace' | 'reconcile' = 'reconcile') => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      try {
        const detail = await fetchSession(sessionId, pageSize, 0, controller.signal, routedPlatform);
        if (cancelled || controller.signal.aborted) return false;
        events.flush();
        dispatch({ type: 'load', view: viewFromDetail(sessionId, detail), mode });
        setTotalMessages(detail.totalMessages || detail.session.messageCount || 0);
        setLoadError(null);

        setCachedSession(sessionId, {
          session: {
            ...detail.session,
            contextTokenCount: detail.session.contextTokenCount ?? detail.contextTokenCount,
          },
          sessionTree: detail.sessionTree,
          messages: detail.messages,
          parts: detail.parts,
          totalMessages: detail.totalMessages || detail.session.messageCount || 0,
          contextTokenCount: detail.contextTokenCount,
          defaultAgent: detail.defaultAgent,
          defaultModel: detail.defaultModel,
          warnings: detail.warnings,
        });
        // Once the load lands, drop out of `loading` if SSE hasn't
        // yet flipped to `live`.
        setStatus((prev) => (prev === 'loading' ? 'live' : prev));
        setLoading(false);
        return true;
      } catch (err) {
        if (cancelled || controller.signal.aborted) return false;
        if (err instanceof DOMException && err.name === 'AbortError') return false;
        setLoadError(err instanceof Error ? err.message : 'Failed to load session');
        setStatus('error');
        setLoading(false);
        return false;
      }
    };

    const events = createSessionSse({
      sessionId,
      dispatch,
      reconcile: () => doFetch('reconcile'),
      dirty: () => setChangesDirtyTick((t) => t + 1),
      debug: debug ? (rows) => setSseDebugEvents((prev) => [...prev, ...rows].slice(-50)) : undefined,
    });

    // The public `reload()` is for user-explicit retry. Use the
    // wholesale-replace mode so the user sees a true authoritative
    // refresh (matches the URL-bar refresh affordance the user
    // would otherwise use).
    reloadRef.current = async () => { await doFetch('replace'); };

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
        // On reconnect (not initial open) refetch so any events
        // emitted during the gap reconcile in one shot. Use the
        // reconcile mode so we don't clobber any in-memory state
        // the server's response hasn't caught up with.
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
      if (reconnectTimer) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
      evtSource?.close();
      evtSource = null;
      setSseNextRetryAt(null);
      connect();
    };

    void doFetch();
    connect();

    return () => {
      cancelled = true;
      const pending = events.dispose();
      // The mirror cleanup has already saved the last rendered view. Preserve
      // events still waiting for a frame without dispatching into the next session.
      if (pending.length && viewRef.current.sessionId === sessionId) {
        const outgoing = reduceBatchedSessionView(viewRef.current, { type: 'sseBatch', events: pending });
        updateCachedSession(sessionId, (prev) => ({
          ...prev, messages: outgoing.messages, parts: outgoing.parts,
        }));
      }
      abortRef.current?.abort();
      abortRef.current = null;
      loadMoreAbortRef.current?.abort();
      loadMoreAbortRef.current = null;
      // Abandon the page rather than wait for it: `fetchSession` is an
      // injected seam, and an implementation that ignores the signal
      // never reaches loadMore's `finally`. Leaving these set would
      // disable pagination for good and pin the spinner on.
      loadMoreInFlightRef.current = false;
      setLoadingMore(false);
      evtSource?.close();
      evtSource = null;
      if (reconnectTimer) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
      retryNowRef.current = () => {};
      setSseReconnecting(false);
      setSseReconnectAttempt(0);
      setSseNextRetryAt(null);
      setSseDebugEvents([]);
    };
  // We deliberately depend on sessionId/routedPlatform only. Options are captured
  // via closure on mount; tests don't change them at runtime and
  // production code never overrides them. Stable function/value
  // refs (fetchSession / reconnectDelay / store setters) are
  // module-level identity.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, routedPlatform]);

  // loadMore prepends an older page. Mirrors the legacy
  // useSessionMessages.loadMore but writes through the reducer via
  // a synthesised `load` action that preserves delta-owned fields.
  const loadMore = useCallback(async () => {
    // The ref, not the `loadingMore` state, is what makes this
    // re-entrant-safe: two calls in the same tick both see the
    // pre-render state value.
    if (!sessionId || loadMoreInFlightRef.current) return;
    loadMoreInFlightRef.current = true;
    setLoadingMore(true);
    const offset = platformMessageCount(viewRef.current.messages);
    // No abort of a previous controller here: the in-flight guard above
    // means there is nothing to abort, and the cleanup already nulls the
    // ref when it abandons a page.
    const controller = new AbortController();
    loadMoreAbortRef.current = controller;
    try {
      const detail = await fetchSession(sessionId, pageSize, offset, controller.signal, routedPlatform);
      if (controller.signal.aborted) return;
      // Re-read the view instead of using the pre-fetch snapshot: SSE
      // may have appended messages while the page was in flight, and
      // a navigation may have replaced the view entirely. Dispatching
      // the stale snapshot would drop the former and splice the old
      // session's history into the new one.
      const current = viewRef.current;
      if (current.sessionId !== sessionId) return;
      if (detail.session.id !== sessionId) return;
      const newMsgs = detail.messages || [];
      const newParts = detail.parts || [];
      if (newMsgs.length === 0) return;
      const existingIds = new Set(current.messages.map((m) => m.id));
      const existingPartIds = new Set(current.parts.map((p) => p.id));
      const uniqueMsgs = newMsgs.filter((m) => !existingIds.has(m.id));
      const uniqueParts = newParts.filter((p) => !existingPartIds.has(p.id));
      dispatch({
        type: 'load',
        view: {
          ...current,
          messages: [...uniqueMsgs, ...current.messages],
          parts: [...uniqueParts, ...current.parts],
        },
      });
      setTotalMessages((prev) => Math.max(prev, detail.totalMessages || detail.session.messageCount || 0));
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      // Surface but don't clobber loadError — the head load is more
      // important.
      remoteLog.error('loadMore failed', err);
    } finally {
      if (loadMoreAbortRef.current === controller) loadMoreAbortRef.current = null;
      loadMoreInFlightRef.current = false;
      setLoadingMore(false);
    }
  }, [sessionId, fetchSession, pageSize, routedPlatform]);

  const hydrateHistory = useCallback((messages: Message[], parts: Part[]) => {
    const current = viewRef.current;
    const currentMessages = new Map(current.messages.map((message) => [message.id, message]));
    const currentParts = new Map(current.parts.map((part) => [part.id, part]));
    const fetchedMessageIDs = new Set(messages.map((message) => message.id));
    const fetchedPartIDs = new Set(parts.map((part) => part.id));
    dispatch({
      type: 'load',
      view: {
        ...current,
        messages: [
          ...messages.map((message) => currentMessages.get(message.id) ?? message),
          ...current.messages.filter((message) => !fetchedMessageIDs.has(message.id)),
        ],
        parts: [
          ...parts.map((part) => currentParts.get(part.id) ?? part),
          ...current.parts.filter((part) => !fetchedPartIDs.has(part.id)),
        ],
      },
    });
    setTotalMessages((total) => Math.max(total, messages.length));
  }, []);

  return {
    ...view,
    status,
    reload,
    loadMore,
    hydrateHistory,
    loading,
    loadingMore,
    loadError,
    totalMessages,
    sseReconnecting,
    sseReconnectAttempt,
    sseNextRetryAt,
    retryNow,
    sseDebugEvents,
    changesDirtyTick,
    clearPrompt,
    setPendingPermission,
    setPendingQuestion,
    patchSession,
    dispatch,
  };
}

export function platformMessageCount(messages: Message[]): number {
  return messages.reduce((count, message) => count + (message.data?.role === 'notice' ? 0 : 1), 0);
}
