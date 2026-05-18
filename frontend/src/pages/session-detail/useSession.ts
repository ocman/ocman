// React hook that wraps the pure `sessionReducer` with the
// EventSource lifecycle described in spec/sse-rewrite/architecture.md.
//
// One reducer, one effect keyed on session id. On mount the hook
// fetches `/api/session/{id}`, opens `/api/session/{id}/events`, and
// dispatches every SSE message through the reducer. On error it
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

import { useCallback, useEffect, useReducer, useRef, useState } from 'react';
import { api, type SessionDetail } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import {
  initialSessionView,
  reduceSessionView,
  type SessionView,
  type SseEvent,
} from '../../lib/sessionReducer';
import { computeReconnectDelay } from './sseBackoff';
import { truncateSseData } from '../../lib/sseHelpers';

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
  fetchSession?: (id: string, limit: number, offset: number, signal?: AbortSignal) => Promise<SessionDetail>;
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
}

export interface UseSessionResult extends SessionView {
  status: UseSessionStatus;
  /** Force-refetch /api/session/{id} and replace state. */
  reload: () => Promise<void>;
  /** Prepend an older page. Idempotent across overlapping ids. */
  loadMore: () => Promise<void>;
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
   * Timestamp of the most recent work-producing SSE event, or null.
   * Drives the busy→waiting debounce in useSessionStatus.
   */
  recentWorkEventAt: number | null;
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
   *  detail reverse sync when poll discovers a prompt SSE missed. */
  setPendingPermission: (perm: import('../../lib/sseHelpers').PendingPermission | null) => void;
  /** Imperatively set a pending question. Same rationale as
   *  setPendingPermission. */
  setPendingQuestion: (q: import('../../components/session/QuestionPrompt').PendingQuestion | null) => void;
  /** Apply a partial patch to the session metadata. Used for
   *  page-local self-mutations like rename / mark-seen. */
  patchSession: (patch: Partial<import('../../lib/sessionReducer').SessionMetadata>) => void;
}

const DEFAULT_PAGE_SIZE = 30;
const WORK_BUMP_THROTTLE_MS = 100;
const DEBUG_RING_SIZE = 50;

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
): Promise<SessionDetail> {
  return api.session(id, limit, offset, signal);
}

/** Build a SessionView from a freshly-fetched SessionDetail. */
function viewFromDetail(id: string, detail: SessionDetail): SessionView {
  return {
    ...initialSessionView(id),
    session: {
      ...detail.session,
      contextTokenCount: detail.session.contextTokenCount ?? detail.contextTokenCount,
      defaultAgent: detail.defaultAgent,
      defaultModel: detail.defaultModel,
    },
    messages: detail.messages ?? [],
    parts: detail.parts ?? [],
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
  const fetchSession = options.fetchSession ?? defaultFetchSession;
  const reconnectDelay = options.reconnectDelay ?? computeReconnectDelay;
  const pageSize = options.pageSize ?? DEFAULT_PAGE_SIZE;
  const maxMessages = options.maxMessages ?? 0;
  const trimTo = options.trimTo ?? maxMessages;
  const debug = options.debug ?? false;

  // Read cache once at mount via getState() — we want a snapshot,
  // not a reactive subscription. Subscribing here would cause every
  // cache write (mirror effect) to re-run this hook's setup.
  const cached = sessionId ? useApiStore.getState().getCachedSession(sessionId) : null;
  const initialView: SessionView = cached
    ? {
        ...initialSessionView(sessionId!),
        session: {
          ...cached.session,
          contextTokenCount: cached.session.contextTokenCount ?? cached.contextTokenCount,
          defaultAgent: cached.defaultAgent,
          defaultModel: cached.defaultModel,
        },
        messages: cached.messages,
        parts: cached.parts,
      }
    : initialSessionView(sessionId ?? '');

  const [view, dispatch] = useReducer(reduceSessionView, initialView);
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
  const [recentWorkEventAt, setRecentWorkEventAt] = useState<number | null>(null);
  const [changesDirtyTick, setChangesDirtyTick] = useState(0);

  // Refs hold non-reactive pieces. retryNowRef / reloadRef expose
  // imperative handles across the effect-closure boundary without
  // forcing the effect to re-run.
  const abortRef = useRef<AbortController | null>(null);
  const reloadRef = useRef<() => Promise<void>>(async () => {});
  const retryNowRef = useRef<() => void>(() => {});
  const workBumpAtRef = useRef<number>(0);
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
    (perm: import('../../lib/sseHelpers').PendingPermission | null) => {
      // Synthesise a permission.asked event when setting; route
      // through `clearPrompt` when clearing. Both go via the
      // reducer so the view-state mutation surface stays single-
      // entry.
      if (perm === null) {
        // No id to clear by — page-level callers always know the id;
        // they should use clearPrompt directly.
        return;
      }
      dispatch({
        type: 'sse',
        event: {
          type: 'permission.asked',
          properties: {
            id: perm.permissionId,
            permission: perm.permission,
            patterns: perm.patterns,
            sessionID: perm.sessionId,
          },
        },
      });
    },
    [],
  );
  const setPendingQuestion = useCallback(
    (q: import('../../components/session/QuestionPrompt').PendingQuestion | null) => {
      if (q === null) return;
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
    lastTrimmedLengthRef.current = view.messages.length;
    const retained = view.messages.slice(-trimTo);
    const retainedIds = new Set(retained.map((m) => m.id));
    const retainedParts = view.parts.filter((p) => retainedIds.has(p.messageId));
    dispatch({
      type: 'load',
      view: {
        ...view,
        messages: retained,
        parts: retainedParts,
      },
    });
  }, [view, maxMessages, trimTo]);

  // Cache mirror — write the latest view into the per-session cache
  // so revisits render instantly. No-ops when the session isn't
  // cached. Only runs after the first successful load.
  useEffect(() => {
    if (!sessionId || !view.session) return;
    const { defaultAgent, defaultModel, ...sessionForCache } = view.session;
    void defaultAgent;
    void defaultModel;
    updateCachedSession(sessionId, (prev) => ({
      ...prev,
      session: sessionForCache,
      messages: view.messages,
      parts: view.parts,
      totalMessages: Math.max(prev.totalMessages ?? 0, totalMessages),
    }));
  }, [sessionId, view.session, view.messages, view.parts, totalMessages, updateCachedSession]);

  useEffect(() => {
    if (!sessionId) {
      setStatus('loading');
      return;
    }

    let cancelled = false;
    let evtSource: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let attempt = 0;
    let hasConnectedOnce = false;

    const bumpWorkEvent = () => {
      const now = Date.now();
      if (now - workBumpAtRef.current < WORK_BUMP_THROTTLE_MS) return;
      workBumpAtRef.current = now;
      setRecentWorkEventAt(now);
    };

    // Refetch trigger for `session.diff` events. The server emits
    // these when an edit/write tool's `state.metadata.filediff` is
    // ready; without a fast refetch the user sees an empty tool
    // block until `session.idle` lands at end-of-turn. We
    // intentionally fire immediately (not debounced) so the block
    // populates as soon as the diff is ready, even mid-turn. The
    // doFetch() AbortController guarantees that overlapping diff
    // events cancel the previous in-flight fetch — at most one
    // request is ever pending.
    const scheduleDiffRefetch = () => {
      if (cancelled) return;
      void doFetch('reconcile');
    };

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
        const detail = await fetchSession(sessionId, pageSize, 0, controller.signal);
        if (cancelled || controller.signal.aborted) return;
        dispatch({ type: 'load', view: viewFromDetail(sessionId, detail), mode });
        setTotalMessages(detail.totalMessages || detail.session.messageCount || 0);
        setLoadError(null);
        setCachedSession(sessionId, {
          session: {
            ...detail.session,
            contextTokenCount: detail.session.contextTokenCount ?? detail.contextTokenCount,
          },
          messages: detail.messages,
          parts: detail.parts,
          totalMessages: detail.totalMessages || detail.session.messageCount || 0,
          contextTokenCount: detail.contextTokenCount,
          defaultAgent: detail.defaultAgent,
          defaultModel: detail.defaultModel,
        });
        // Once the load lands, drop out of `loading` if SSE hasn't
        // yet flipped to `live`.
        setStatus((prev) => (prev === 'loading' ? 'live' : prev));
        setLoading(false);
      } catch (err) {
        if (cancelled || controller.signal.aborted) return;
        if (err instanceof DOMException && err.name === 'AbortError') return;
        setLoadError(err instanceof Error ? err.message : 'Failed to load session');
        setStatus('error');
        setLoading(false);
      }
    };

    // The public `reload()` is for user-explicit retry. Use the
    // wholesale-replace mode so the user sees a true authoritative
    // refresh (matches the URL-bar refresh affordance the user
    // would otherwise use).
    reloadRef.current = () => doFetch('replace');

    const connect = () => {
      if (cancelled) return;
      evtSource = new EventSource(`/api/session/${encodeURIComponent(sessionId)}/events`);
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
        if (hasConnectedOnce) {
          void doFetch('reconcile');
        }
        hasConnectedOnce = true;
      };
      evtSource.onmessage = (evt) => {
        if (cancelled) return;
        const raw = evt.data || '';
        if (!raw || !raw.trim()) return;
        if (debug) {
          setSseDebugEvents((prev) => {
            const next = [...prev, { at: Date.now(), event: 'message', data: truncateSseData(raw) }];
            return next.slice(-DEBUG_RING_SIZE);
          });
        }
        let parsed: SseEvent;
        try {
          parsed = JSON.parse(raw) as SseEvent;
        } catch {
          return;
        }
        // Temporary diagnostic: log every event under ?debug so we
        // can see the wire shape when the user reports "tool block
        // doesn't show until refresh". Remove once the issue is
        // resolved.
        if (debug) {
          console.log('[ocman:sse]', parsed.type, parsed.properties);
        }
        dispatch({ type: 'sse', event: parsed });

        // Derive bumped/dirtied signals from event type. The
        // reducer itself stays platform-agnostic; these effects
        // are SSE-handler-side only.
        if (
          parsed.type === 'message.created' ||
          parsed.type === 'message.updated' ||
          parsed.type === 'message.part.updated' ||
          parsed.type === 'message.part.delta'
        ) {
          bumpWorkEvent();
        }
        if (parsed.type === 'message.part.updated') {
          const props = parsed.properties || {};
          const part = props.part as Record<string, unknown> | undefined;
          if (part && part.type === 'tool') {
            const tool = part.tool as string | undefined;
            if (tool && (
              tool === 'edit' || tool === 'write' ||
              tool === 'mcp_edit' || tool === 'mcp_write' ||
              tool === 'mcp_Edit' || tool === 'mcp_Write'
            )) {
              setChangesDirtyTick((t) => t + 1);
            }
          }
        }
        // `session.diff` carries the per-file diff payload for the
        // right-panel changes view. We bump changesDirtyTick so the
        // panel refreshes its REST call, and (debounced) trigger a
        // refetch of /api/session/{id} so any updated tool-part
        // metadata (state.metadata.filediff on edit/write parts)
        // lands in the thread without the user having to refresh.
        // Without this, the user sees "tool blocks don't show until
        // refresh" — the part exists in messages but its `filediff`
        // metadata is only filled in on the server after the diff
        // event lands. See spec/sse-rewrite for the canonical event
        // table.
        if (parsed.type === 'session.diff') {
          setChangesDirtyTick((t) => t + 1);
          scheduleDiffRefetch();
        }
        // `session.idle` and `session.status: idle` request a refetch
        // (the reducer sets _refetchRequested; we observe via the
        // event type because we can't read view synchronously here).
        if (
          parsed.type === 'session.idle' ||
          (parsed.type === 'session.status' && isIdleStatus(parsed))
        ) {
          void doFetch('reconcile');
        }
      };
      // OpenCode sometimes emits events on named SSE channels in
      // addition to the default `message` channel. Listen for the
      // known channel names so we don't silently drop them. The
      // handler routes everything through the same reducer.
      const handleNamedEvent = (eventName: string) => (evt: MessageEvent) => {
        if (cancelled) return;
        const raw = evt.data || '';
        if (!raw || !raw.trim()) return;
        if (debug) {
          setSseDebugEvents((prev) => {
            const next = [...prev, { at: Date.now(), event: eventName, data: truncateSseData(raw) }];
            return next.slice(-DEBUG_RING_SIZE);
          });
        }
        let parsed: SseEvent;
        try {
          parsed = JSON.parse(raw) as SseEvent;
        } catch {
          return;
        }
        // Some servers omit the `type` field when using a named
        // channel (the event name IS the type). Fill it in so the
        // reducer's switch matches.
        if (!parsed.type) {
          parsed = { ...parsed, type: eventName };
        }
        if (debug) {
          console.log('[ocman:sse:' + eventName + ']', parsed.type, parsed.properties);
        }
        dispatch({ type: 'sse', event: parsed });
      };
      // Known OpenCode named event channels. Do not include the
      // default `message` channel here: browsers deliver that channel
      // to both `onmessage` and `addEventListener('message')`, so
      // registering both appends every live delta twice. Refresh is
      // unaffected because REST snapshots only go through `doFetch`.
      [
        'message.created',
        'message.updated',
        'message.part.updated',
        'message.part.delta',
        'session.status',
        'session.idle',
        'session.diff',
        'permission',
        'permission.asked',
        'permission.replied',
        'question',
        'question.asked',
        'question.replied',
        'question.rejected',
        'approval',
        'tool',
        'error',
      ].forEach((name) => {
        evtSource?.addEventListener(name, handleNamedEvent(name));
      });
      evtSource.onerror = () => {
        if (cancelled) return;
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
      abortRef.current?.abort();
      abortRef.current = null;
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
      setRecentWorkEventAt(null);
      workBumpAtRef.current = 0;
    };
  // We deliberately depend on sessionId only. Options are captured
  // via closure on mount; tests don't change them at runtime and
  // production code never overrides them. Stable function/value
  // refs (fetchSession / reconnectDelay / store setters) are
  // module-level identity.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  // loadMore prepends an older page. Mirrors the legacy
  // useSessionMessages.loadMore but writes through the reducer via
  // a synthesised `load` action that preserves delta-owned fields.
  const loadMore = useCallback(async () => {
    if (!sessionId || loadingMore) return;
    setLoadingMore(true);
    const current = viewRef.current;
    const offset = current.messages.length;
    const controller = new AbortController();
    try {
      const detail = await fetchSession(sessionId, pageSize, offset, controller.signal);
      if (controller.signal.aborted) return;
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
      console.error('loadMore failed', err);
    } finally {
      setLoadingMore(false);
    }
  }, [sessionId, loadingMore, fetchSession, pageSize]);

  return {
    ...view,
    status,
    reload,
    loadMore,
    loading,
    loadingMore,
    loadError,
    totalMessages,
    sseReconnecting,
    sseReconnectAttempt,
    sseNextRetryAt,
    retryNow,
    sseDebugEvents,
    recentWorkEventAt,
    changesDirtyTick,
    clearPrompt,
    setPendingPermission,
    setPendingQuestion,
    patchSession,
  };
}

/**
 * True for `session.status` events whose payload reports idle.
 */
function isIdleStatus(event: SseEvent): boolean {
  const props = event.properties;
  if (!props) return false;
  const status = props.status;
  if (typeof status === 'string') return status === 'idle';
  if (status && typeof status === 'object' && !Array.isArray(status)) {
    const t = (status as Record<string, unknown>).type;
    return typeof t === 'string' && t === 'idle';
  }
  return false;
}
