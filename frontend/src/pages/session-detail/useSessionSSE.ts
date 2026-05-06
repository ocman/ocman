import { useCallback, useEffect, useRef, useState } from 'react';
import type { Dispatch, MutableRefObject, SetStateAction } from 'react';
import type { Message, Part } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import {
  extractMessageFromEvent,
  extractPartFromEvent,
  extractPendingPermission,
  extractPendingQuestion,
  isSessionStatusIdle,
  truncateSseData,
  type PendingPermission,
} from '../../lib/sseHelpers';
import { computeReconnectDelay } from './sseBackoff';
import {
  inferStatusFromMessage,
  insertMessageByTime,
  mergeParts,
  truncatePartField,
  upsertPart,
} from '../../lib/sseMessageHelpers';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';
import { isSessionRelevant } from '../../lib/promptRouting';
import { storePendingQuestion, clearPendingQuestion } from './usePromptHandlers';
import type { SessionWithDefaults } from '../../lib/sessionStatus';
import type { SubagentTokenMap } from './useSubagentTracking';
import { trackRender } from '../../lib/renderRateMonitor';
import { createSseDeltaBuffer } from './sseDeltaBuffer';

/** Single SSE event row in the debug overlay. */
export interface SseDebugEvent {
  at: number;
  event: string;
  data: string;
}

export interface UseSessionSSEOptions {
  /** Session id whose events to subscribe to. Effect short-circuits
   *  when undefined. */
  sessionId: string | undefined;
  /** Session directory — included as a stable effect dep so the
   *  effect tears down/reopens on session swap. */
  directory: string | undefined;
  /** Reload the latest message page from the API. Used after SSE
   *  errors / reconnects / idle signals to reconcile state. */
  load: (signal?: AbortSignal) => Promise<void>;
  /** AbortController shared with the rest of the page; the SSE
   *  effect reads its signal at call time. */
  abortSignalRef: MutableRefObject<AbortController | null>;
  /** Mirror of `loadError` used to gate the reconciliation fetch
   *  (see spec/session-switch-cache step 5). */
  loadErrorRef: MutableRefObject<string | null>;
  /** Mirror of debugMode (the URL `?debug` flag) for the inline
   *  SSE event log. */
  debugModeRef: MutableRefObject<boolean>;
  /** Set of subagent session ids whose events bubble up to this page. */
  subagentSessionIdsRef: MutableRefObject<Set<string>>;
  /** Page state mutators. The hook holds none of this state itself
   *  — the SSE handler writes through to the page so the rest of
   *  the UI re-renders synchronously. */
  setMessages: Dispatch<SetStateAction<Message[]>>;
  setParts: Dispatch<SetStateAction<Part[]>>;
  setSession: Dispatch<SetStateAction<SessionWithDefaults | null>>;
  setPortAvailable: Dispatch<SetStateAction<boolean>>;
  setPendingPermission: Dispatch<SetStateAction<PendingPermission | null>>;
  setPermissionError: Dispatch<SetStateAction<string | null>>;
  setPendingQuestion: Dispatch<SetStateAction<PendingQuestion | null>>;
  setSubagentTokens: Dispatch<SetStateAction<SubagentTokenMap>>;
  setChangesDirtyTick: Dispatch<SetStateAction<number>>;
  /** Current route session id. Async callbacks must still belong to
   *  this id before mutating page state. */
  activeSessionIdRef: MutableRefObject<string | undefined>;
}

export interface UseSessionSSEResult {
  /**
   * Epoch-ms timestamp of the most recent work-producing event for
   * the session tree (current session + bubbled subagent activity),
   * or `null` when none has landed since connect/reset.
   */
  recentWorkEventAt: number | null;
  /** True while the SSE EventSource reports `OPEN`. The composer
   *  uses this to surface a "reconnecting…" pill when SSE drops
   *  even though portAvailable stayed true. */
  sseActive: boolean;
  /** True from the moment the EventSource errors out until it
   *  reconnects. Distinguishes "we briefly lost contact and are
   *  trying again" from the cold-start case where SSE never came up
   *  in the first place. */
  sseReconnecting: boolean;
  /** 1-based count of consecutive reconnect attempts since the last
   *  successful open. Resets to 0 on `onopen`. The UI uses this to
   *  show "attempt N" alongside the reconnecting indicator. */
  sseReconnectAttempt: number;
  /** Epoch-ms timestamp at which the next reconnect attempt is
   *  scheduled, or `null` when no reconnect is pending. The UI
   *  derives a live countdown from this value. */
  sseNextRetryAt: number | null;
  /** Cancel the pending backoff timer and reconnect immediately.
   *  Wired to a "Retry now" link in the reconnecting indicator so
   *  users don't have to wait out the (capped) one-minute interval
   *  after a long outage. No-op when no reconnect is pending. */
  retryNow: () => void;
  /** Last 50 SSE events as captured for the debug overlay. Empty
   *  when `?debug` is not set. */
  sseDebugEvents: SseDebugEvent[];
  /** Setter for the debug overlay buffer. Exposed so the page's
   *  session-change effect can clear it on navigation. */
  setSseDebugEvents: Dispatch<SetStateAction<SseDebugEvent[]>>;
}

/**
 * Owns the live SSE subscription for the page session. Connects to
 * `/api/session/{id}/events`, processes message / part / permission /
 * question events incrementally, and reconnects with exponential
 * backoff (500 ms → 60 s, equal jitter) on errors. Falls back to a
 * 10 s polling load when the EventSource isn't open. Reconnect
 * progress is exposed via `sseReconnecting` / `sseReconnectAttempt`
 * / `sseNextRetryAt` / `retryNow` so the page can render an inline
 * "reconnecting…" indicator with a manual retry escape hatch.
 *
 * Behaviour notes:
 *   - On `onopen`, fetches any pending permissions / questions that
 *     existed before the connection (those are not delivered as
 *     events) and triggers a reconciliation `load()` after the
 *     first reconnect or when the initial load failed.
 *   - Routes events from subagent sessions back to this page when
 *     the subagent's id is in `subagentSessionIdsRef`. Token data
 *     from subagent assistant messages feeds the TPS indicator.
 *   - `message.created`, `message.updated`, `message.part.updated`
 *     and `message.part.delta` write through the message / part /
 *     session setters using the canonical helpers from
 *     lib/sseMessageHelpers and lib/sseHelpers.
 *   - A catch-all branch tries three extraction strategies for
 *     unknown event names so legacy / future SSE shapes still
 *     deliver content updates.
 */
export function useSessionSSE({
  sessionId,
  directory: _directory,
  load,
  abortSignalRef,
  loadErrorRef,
  debugModeRef,
  subagentSessionIdsRef,
  setMessages,
  setParts,
  setSession,
  setPortAvailable,
  setPendingPermission,
  setPermissionError,
  setPendingQuestion,
  setSubagentTokens,
  setChangesDirtyTick,
  activeSessionIdRef,
}: UseSessionSSEOptions): UseSessionSSEResult {
  trackRender('useSessionSSE', { sessionId });
  const listPermissions = useApiStore((s) => s.listPermissions);
  const listQuestions = useApiStore((s) => s.listQuestions);

  const [sseActive, setSseActive] = useState(false);
  const [sseReconnecting, setSseReconnecting] = useState(false);
  const [sseReconnectAttempt, setSseReconnectAttempt] = useState(0);
  const [sseNextRetryAt, setSseNextRetryAt] = useState<number | null>(null);
  const [sseDebugEvents, setSseDebugEvents] = useState<SseDebugEvent[]>([]);
  const [recentWorkEventAt, setRecentWorkEventAt] = useState<number | null>(null);
  const recentWorkEventBumpRef = useRef<number>(0);

  // The effect installs its imperative "reconnect right now" handle
  // here so the public `retryNow` callback can reach across the
  // closure boundary without re-running the whole effect.
  const retryNowRef = useRef<(() => void) | null>(null);
  const retryNow = useCallback(() => {
    retryNowRef.current?.();
  }, []);

  useEffect(() => {
    if (!sessionId) return;
    const sid = sessionId;
    let evtSource: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;
    let hasReceivedContentEvent = false;
    let hasConnectedOnce = false;
    // Counter for the exponential-backoff schedule. Lives outside
    // React state because both `onerror` and `retryNow` need to read
    // and mutate it synchronously without waiting for a re-render.
    let attempt = 0;
    const isCurrentSession = () => !cancelled && activeSessionIdRef.current === sid;

    const loadNow = () => {
      if (!isCurrentSession()) return;
      const signal = abortSignalRef.current?.signal;
      load(signal);
    };
    const WORK_BUMP_THROTTLE_MS = 100;
    const bumpRecentWorkEventAt = () => {
      const now = Date.now();
      if (now - recentWorkEventBumpRef.current < WORK_BUMP_THROTTLE_MS) return;
      recentWorkEventBumpRef.current = now;
      setRecentWorkEventAt(now);
    };

    // Coalesces `message.part.delta` events into one setParts call
    // per animation frame. This keeps the streaming UI live (each
    // delta still paints on the very next frame) while capping the
    // React commit frequency at the browser paint rate, which keeps
    // user-initiated updates (clicks, navigation) responsive even
    // while the wire is hot. See sseDeltaBuffer.ts for details.
    const deltaBuffer = createSseDeltaBuffer(setParts);

    // Process a parsed SSE event: handle permission/question prompts
    // and clear stale prompts. Only triggers state updates when
    // values change.
    const handleParsedEvent = (parsed: Record<string, unknown>) => {
      const type = (parsed.type as string) || '';

      const perm = extractPendingPermission(parsed);
      if (perm) {
        setPendingPermission(perm);
        setPermissionError(null);
      }
      const question = extractPendingQuestion(parsed);
      if (question) {
        const questionSid = question.sessionID || sid;
        storePendingQuestion(questionSid, question);
        if (!question.sessionID || question.sessionID === sid) {
          setPendingQuestion(question);
        }
      }
      // Clear permission/question state on specific event types
      // (not on message.updated — that fires for queued user
      // messages and would prematurely dismiss the prompt).
      if (type === 'permission.replied') {
        const props = parsed.properties as Record<string, unknown> | undefined;
        const repliedId =
          (typeof props?.requestID === 'string' && props.requestID) ||
          (typeof props?.requestId === 'string' && props.requestId) ||
          (typeof props?.id === 'string' && props.id) ||
          (typeof props?.permissionID === 'string' && props.permissionID) ||
          '';
        setPendingPermission((prev) => {
          if (prev === null) return prev;
          if (!repliedId) return prev;
          return prev.permissionId === repliedId ? null : prev;
        });
      } else if (type === 'session.idle' || (type === 'session.status' && isSessionStatusIdle(parsed))) {
        // Only clear on terminal session states.
        setPendingPermission((prev) => prev === null ? prev : null);
      }
      if (
        type === 'question.replied' ||
        type === 'question.rejected' ||
        type === 'session.idle' ||
        (type === 'session.status' && isSessionStatusIdle(parsed))
      ) {
        setPendingQuestion((prev) => {
          if (prev === null) return prev;
          clearPendingQuestion(sid);
          return null;
        });
      }
    };

    const connect = () => {
      if (cancelled) return;
      evtSource = new EventSource(`/api/session/${encodeURIComponent(sid)}/events`);
      evtSource.onopen = () => {
        if (!isCurrentSession()) return;
        setSseActive(true);
        // Successful (re)connect: clear all reconnect bookkeeping so
        // the indicator disappears and the next disconnect starts a
        // fresh exponential schedule.
        attempt = 0;
        setSseReconnecting(false);
        setSseReconnectAttempt(0);
        setSseNextRetryAt(null);
        // SSE connected means OpenCode is running — mark port as available.
        setPortAvailable(true);
        // Fetch any permissions that were already pending when we
        // connected. SSE only delivers new events; existing pending
        // permissions need to be retrieved explicitly so the dialog
        // shows immediately.
        listPermissions(sid).then((perms) => {
          if (!isCurrentSession()) return;
          for (const p of perms) {
            const perm = extractPendingPermission({ type: 'permission.asked', properties: p });
            if (!perm) continue;
            const props = p as Record<string, unknown>;
            const promptSid = typeof props.sessionID === 'string' ? props.sessionID : '';
            if (!isSessionRelevant(promptSid, sid, subagentSessionIdsRef.current)) continue;
            setPendingPermission(perm);
            setPermissionError(null);
            break;
          }
        }).catch(() => { /* ignore — SSE will deliver live permissions */ });
        listQuestions(sid).then((questions) => {
          if (!isCurrentSession()) return;
          for (const q of questions) {
            const question = extractPendingQuestion({ type: 'question.asked', properties: q });
            if (!question) continue;
            const props = q as Record<string, unknown>;
            const questionSid = typeof props.sessionID === 'string' ? props.sessionID : '';
            if (!isSessionRelevant(questionSid, sid, subagentSessionIdsRef.current)) continue;
            storePendingQuestion(sid, question);
            setPendingQuestion((prev) => prev ?? question);
            break;
          }
        }).catch(() => { /* ignore — SSE will deliver live questions */ });
        // Reconciliation: fetch the latest state only when the
        // initial load() failed AND no SSE content events have
        // arrived. In the happy path the initial load is
        // authoritative and SSE takes over for live updates.
        setTimeout(() => {
          if (!isCurrentSession() || hasReceivedContentEvent) return;
          if (!loadErrorRef.current) return;
          const signal = abortSignalRef.current?.signal;
          load(signal);
        }, 500);
        // Reconnect reconciliation: on every reconnect (not the
        // first connection), refetch authoritative session state.
        if (hasConnectedOnce) {
          const signal = abortSignalRef.current?.signal;
          load(signal);
        }
        hasConnectedOnce = true;
      };
      evtSource.onmessage = (evt) => {
        if (!isCurrentSession()) return;
        const raw = evt.data || '';
        if (!raw || !raw.trim()) return;

        if (debugModeRef.current) {
          setSseDebugEvents((prev) => {
            const next = [...prev, { at: Date.now(), event: 'message', data: truncateSseData(raw) }];
            return next.slice(-10);
          });
        }

        let parsed: Record<string, unknown>;
        try {
          parsed = JSON.parse(raw) as Record<string, unknown>;
        } catch {
          return;
        }


        const type = (parsed.type as string) || '';

        // Filter out events for other sessions (or route subagent
        // events to this page when they belong to a known subagent).
        const evtProps = (parsed.properties && typeof parsed.properties === 'object')
          ? parsed.properties as Record<string, unknown>
          : null;
        const evtSessionId: string | undefined =
          (evtProps?.sessionID as string) ||
          ((evtProps?.info as Record<string, unknown> | undefined)?.sessionID as string) ||
          ((evtProps?.part as Record<string, unknown> | undefined)?.sessionID as string) ||
          undefined;
        const isSubagentEvent =
          !!evtSessionId && evtSessionId !== sid && subagentSessionIdsRef.current.has(evtSessionId);
        if (evtSessionId && evtSessionId !== sid) {
          // Capture token data from subagent sessions for the TPS
          // indicator. Track per-message tokens so we can sum
          // accurately across multiple assistant messages within a
          // subagent session.
          if (isSubagentEvent && (type === 'message.created' || type === 'message.updated')) {
            const subInfo = (evtProps?.info || (evtProps as Record<string, unknown>)) as Record<string, unknown> | undefined;
            if (subInfo && typeof subInfo.id === 'string' && subInfo.role === 'assistant') {
              const msgId = subInfo.id as string;
              const subTokens = subInfo.tokens as { input?: number; output?: number } | undefined;
              const subTime = subInfo.time as { created?: number } | undefined;
              if (subTokens?.output || subTime?.created) {
                setSubagentTokens((prev) => {
                  const existing = prev.get(msgId);
                  const output = subTokens?.output || existing?.output || 0;
                  const created = subTime?.created || existing?.created || Date.now();
                  const updated = new Map(prev);
                  updated.set(msgId, {
                    output: Math.max(existing?.output || 0, output),
                    created: existing ? Math.min(existing.created, created) : created,
                  });
                  return updated;
                });
              }
            }
            bumpRecentWorkEventAt();
          }
          if (isSubagentEvent && type === 'message.part.updated') bumpRecentWorkEventAt();
          if (isSubagentEvent && type === 'message.part.delta') bumpRecentWorkEventAt();
          if (isSubagentEvent && type === 'session.status') {
            const statusObj = evtProps?.status as Record<string, unknown> | string | undefined;
            const status = typeof statusObj === 'string'
              ? statusObj
              : (typeof statusObj === 'object' && statusObj !== null)
                ? (statusObj.type as string | undefined)
                : (evtProps?.status as string | undefined);
            if (status === 'busy') bumpRecentWorkEventAt();
          }
          // Subagent prompt events bubble up to the parent session
          // UI so the user can answer them.
          if (isSubagentEvent && (
            type === 'permission.asked' ||
            type === 'permission.replied' ||
            type === 'question.asked' ||
            type === 'question.replied' ||
            type === 'question.rejected'
          )) {
            handleParsedEvent(parsed);
          }
          return;
        }

        handleParsedEvent(parsed);

        // Apply content updates incrementally.
        const props = evtProps;

        if (type === 'message.created') {
          const extracted = extractMessageFromEvent(parsed, sid);
          if (extracted) {
            hasReceivedContentEvent = true;
            if (extracted.message.data.role === 'assistant') bumpRecentWorkEventAt();
            setMessages((prev) => insertMessageByTime(prev, extracted.message));
            if (extracted.parts.length > 0) {
              setParts((prev) => mergeParts(prev, extracted.parts));
            }
            setSession((prev) => {
              if (!prev) return prev;
              if (extracted.message.data.role !== 'assistant') return prev;
              const status = inferStatusFromMessage(extracted.message);
              if (prev.status === status) return prev;
              return { ...prev, status };
            });
          }
        }

        if (type === 'message.updated' && props) {
          const extracted = extractMessageFromEvent(parsed, sid);
          if (extracted) {
            hasReceivedContentEvent = true;
            if (extracted.message.data.role === 'assistant') bumpRecentWorkEventAt();
            setMessages((prev) => insertMessageByTime(prev, extracted.message));
            if (extracted.parts.length > 0) {
              setParts((prev) => mergeParts(prev, extracted.parts));
            }
            setSession((prev) => {
              if (!prev) return prev;
              if (extracted.message.data.role !== 'assistant') return prev;
              const status = inferStatusFromMessage(extracted.message);
              if (prev.status === status) return prev;
              return { ...prev, status };
            });
          } else {
            const info = props.info as Record<string, unknown> | undefined;
            if (info && info.id) {
              hasReceivedContentEvent = true;
              if (info.role === 'assistant') bumpRecentWorkEventAt();
              const msgId = info.id as string;
              setMessages((prev) => {
                const idx = prev.findIndex((m) => m.id === msgId);
                if (idx < 0) return prev;
                const updated = { ...prev[idx] };
                updated.data = {
                  ...updated.data,
                  finish: info.finish as string | undefined,
                  tokens: info.tokens as Message['data']['tokens'],
                  time: (info.time ?? updated.data.time) as Message['data']['time'],
                  cost: typeof info.cost === 'number' ? info.cost : updated.data.cost,
                  error: info.error as Message['data']['error'],
                };
                const next = [...prev];
                next[idx] = updated;
                return next;
              });
              setSession((prev) => {
                if (!prev) return prev;
                const role = info.role as string | undefined;
                if (role !== 'assistant') return prev;
                const status = inferStatusFromMessage({
                  id: msgId,
                  sessionId: sid,
                  timeCreated: 0,
                  data: {
                    role: 'assistant',
                    finish: info.finish as string | undefined,
                    error: info.error as Message['data']['error'],
                  },
                });
                if (prev.status === status) return prev;
                return { ...prev, status };
              });
            }
          }
        }

        if (type === 'message.part.updated' && props) {
          const rawPart = props.part as Record<string, unknown> | undefined;
          if (rawPart && rawPart.id && rawPart.messageID) {
            hasReceivedContentEvent = true;
            bumpRecentWorkEventAt();
            const partType = rawPart.type as string | undefined;
            if (partType !== 'step-start' && partType !== 'step-finish' && partType !== 'snapshot') {
              if (typeof rawPart.text === 'string') rawPart.text = truncatePartField(rawPart.text) as string;
              const state = rawPart.state as Record<string, unknown> | undefined;
              if (state) {
                if (typeof state.output === 'string') state.output = truncatePartField(state.output) as string;
                const meta = state.metadata as Record<string, unknown> | undefined;
                if (meta && typeof meta.output === 'string') meta.output = truncatePartField(meta.output) as string;
              }
              const part: Part = {
                id: rawPart.id as string,
                messageId: rawPart.messageID as string,
                sessionId: (rawPart.sessionID as string) || sid,
                data: rawPart as unknown as string,
              };
              setParts((prev) => upsertPart(prev, part));
              // Mark the changes sidebar dirty when an edit/write
              // tool part lands. Hooks coalesce successive ticks.
              if (partType === 'tool') {
                const toolName = (rawPart as Record<string, unknown>).tool as string | undefined;
                if (toolName && (
                  toolName === 'edit' || toolName === 'write' ||
                  toolName === 'mcp_edit' || toolName === 'mcp_write' ||
                  toolName === 'mcp_Edit' || toolName === 'mcp_Write'
                )) {
                  setChangesDirtyTick((t) => t + 1);
                }
              }
            }
          }
        }

        if (type === 'message.part.delta' && props) {
          const partId = props.partID as string | undefined;
          const messageId = props.messageID as string | undefined;
          const deltaText = (props.delta as string) || '';
          const field = (props.field as string) || 'text';
          if (partId && messageId && deltaText) {
            hasReceivedContentEvent = true;
            bumpRecentWorkEventAt();
            // Coalesce into the rAF-flushed buffer instead of calling
            // setParts synchronously. The user still sees each delta
            // on the very next frame; we just cap React commits at
            // the browser's paint rate so the scheduler isn't
            // saturated when deltas arrive faster than the browser
            // can paint.
            deltaBuffer.enqueue({
              partId,
              messageId,
              sessionId: (props.sessionID as string) || sid,
              field,
              delta: deltaText,
            });
          }
        }

        // Catch-all for unknown event names that still carry part
        // data — three extraction strategies cover legacy shapes.
        if (
          type !== 'message.created' &&
          type !== 'message.updated' &&
          type !== 'message.part.updated' &&
          type !== 'message.part.delta' &&
          type !== 'session.status'
        ) {
          let handled = false;
          if (props) {
            const part = extractPartFromEvent(parsed, sid);
            if (part) {
              hasReceivedContentEvent = true;
              handled = true;
              setParts((prev) => upsertPart(prev, part));
            }
          }
          if (!handled && props) {
            const rawPart = props.part as Record<string, unknown> | undefined;
            if (rawPart && rawPart.id && rawPart.messageID) {
              hasReceivedContentEvent = true;
              handled = true;
              const partType = rawPart.type as string | undefined;
              if (partType !== 'step-start' && partType !== 'step-finish' && partType !== 'snapshot') {
                if (typeof rawPart.text === 'string') rawPart.text = truncatePartField(rawPart.text) as string;
                const part: Part = {
                  id: rawPart.id as string,
                  messageId: rawPart.messageID as string,
                  sessionId: (rawPart.sessionID as string) || sid,
                  data: rawPart as unknown as string,
                };
                setParts((prev) => upsertPart(prev, part));
              }
            }
          }
          if (!handled) {
            const extracted = extractMessageFromEvent(parsed, sid);
            if (extracted) {
              hasReceivedContentEvent = true;
              if (extracted.message.data.role === 'assistant') bumpRecentWorkEventAt();
              setMessages((prev) => insertMessageByTime(prev, extracted.message));
              if (extracted.parts.length > 0) {
                setParts((prev) => mergeParts(prev, extracted.parts));
              }
            }
          }
        }

        if (type === 'session.status' && props) {
          const statusObj = props.status as Record<string, unknown> | string | undefined;
          const status = typeof statusObj === 'string'
            ? statusObj
            : (typeof statusObj === 'object' && statusObj !== null)
              ? (statusObj.type as string | undefined)
              : (props.status as string | undefined);
          if (status === 'waiting' || status === 'busy' || status === 'done' || status === 'error' || status === 'idle') {
            if (status === 'busy') bumpRecentWorkEventAt();
            const mapped = status === 'idle' ? 'done' : status;
            setSession((prev) => prev && prev.status !== mapped
              ? { ...prev, status: mapped as 'waiting' | 'busy' | 'done' | 'error' }
              : prev,
            );
            if (status === 'idle') {
              loadNow();
            }
          }
        }

        if (type === 'session.idle') {
          loadNow();
        }

        if (type === 'message.updated') {
          loadNow();
        }
      };
      // Some OpenCode SSE updates may use named events, not default
      // "message". Listen for the known custom names too.
      ['question', 'permission', 'approval', 'tool', 'error'].forEach((eventName) => {
        evtSource?.addEventListener(eventName, (evt) => {
          const raw = (evt as MessageEvent).data || '';
          if (!raw) return;
          if (debugModeRef.current) {
            setSseDebugEvents((prev) => {
              const next = [...prev, { at: Date.now(), event: eventName, data: truncateSseData(raw) }];
              return next.slice(-50);
            });
          }
          try {
            const parsed = JSON.parse(raw) as Record<string, unknown>;
            handleParsedEvent(parsed);
          } catch { /* not JSON */ }
        });
      });
      evtSource.onerror = () => {
        setSseActive(false);
        evtSource?.close();
        evtSource = null;
        if (cancelled) return;
        // Exponential backoff with equal jitter: 500 ms → ~1 s → ~2 s
        // → … → 60 s (capped). See sseBackoff.ts for the schedule.
        const delay = computeReconnectDelay(attempt);
        attempt += 1;
        setSseReconnecting(true);
        setSseReconnectAttempt(attempt);
        setSseNextRetryAt(Date.now() + delay);
        reconnectTimer = setTimeout(connect, delay);
      };
    };

    // Imperative handle for the public `retryNow` callback. We expose
    // it through a ref so the cleanup function and the timer logic
    // share the same `reconnectTimer`/`evtSource` closures without
    // having to re-run the effect on every render.
    retryNowRef.current = () => {
      if (cancelled) return;
      if (reconnectTimer) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
      // Tear down any half-open EventSource so we don't end up with
      // two parallel connections after a manual retry.
      evtSource?.close();
      evtSource = null;
      setSseNextRetryAt(null);
      connect();
    };

    connect();

    // Fallback polling when SSE is not active.
    const fallback = setInterval(() => {
      if (!evtSource || evtSource.readyState !== EventSource.OPEN) {
        const signal = abortSignalRef.current?.signal;
        load(signal);
      }
    }, 10000);

    return () => {
      cancelled = true;
      evtSource?.close();
      if (reconnectTimer) clearTimeout(reconnectTimer);
      clearInterval(fallback);
      retryNowRef.current = null;
      // Flush any deltas that arrived between the last paint and the
      // teardown so the user sees the final tokens of the previous
      // session before we tear down. `flush()` is a no-op if there's
      // nothing pending, so this is safe.
      deltaBuffer.flush();
      deltaBuffer.cancel();
      setSseActive(false);
      setSseReconnecting(false);
      setSseReconnectAttempt(0);
      setSseNextRetryAt(null);
      setRecentWorkEventAt(null);
      recentWorkEventBumpRef.current = 0;
    };
    // We deliberately keep the dep list narrow: `directory` and
    // `sessionId` re-open the stream on session swap; `load`,
    // `listPermissions`, `listQuestions` are stable function
    // references owned by the apiStore. Setters are stable too.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [_directory, sessionId, load, listPermissions, listQuestions, activeSessionIdRef]);

  return {
    recentWorkEventAt,
    sseActive,
    sseReconnecting,
    sseReconnectAttempt,
    sseNextRetryAt,
    retryNow,
    sseDebugEvents,
    setSseDebugEvents,
  };
}
