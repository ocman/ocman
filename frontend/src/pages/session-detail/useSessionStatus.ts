import { useEffect, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { Message, Session } from '../../lib/api';
import { deriveRawStatus } from '../../lib/sessionStatus';
import { useSyncRef } from '../../lib/useSyncRef';
import type { PendingPermission } from '../../lib/sseHelpers';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';
import type { SubagentTokenMap } from './useSubagentTracking';
import { trackRender } from '../../lib/renderRateMonitor';

/**
 * How long to keep the session marked `busy` after the underlying
 * raw status flips to `waiting`. Tool-call turn boundaries can flip
 * `busy → waiting → busy` in tens of milliseconds; debouncing the
 * waiting transition keeps the status badge from strobing.
 */
const STATUS_GRACE_MS = 3000;

/**
 * If we received an SSE event for the session within this window,
 * treat the session as actively producing output and surface
 * `busy` even when the last assistant message has flipped to a
 * finished state. This catches the common case where the session
 * is mid-turn but the message snapshot we have hasn't been
 * reconciled yet — a steady stream of part deltas means *something*
 * is still happening.
 *
 * Terminal states (`error`, `done`) are never overridden — the SSE
 * freshness rule only upgrades `waiting → busy`.
 */
const SSE_ACTIVE_MS = 500;

export interface UseSessionStatusOptions {
  /** Most recent message in the page's messages array (or null). */
  lastMsg: Message | null;
  /** Whole messages array — used to scope the TPS window to the
   *  current run (since the last user turn). */
  messages: Message[];
  /** Snapshot of per-message token counts from subagent runs. */
  subagentTokens: SubagentTokenMap;
  /** Setter for the subagent token map; cleared when a run ends. */
  setSubagentTokens: Dispatch<SetStateAction<SubagentTokenMap>>;
  /** Whether the assistant is currently producing output. */
  isRunning: boolean;
  /** Pending permission, when one is waiting on the user. */
  pendingPermission: PendingPermission | null;
  /** Pending question, when one is waiting on the user. */
  pendingQuestion: PendingQuestion | null;
  /**
   * Epoch-ms timestamp of the last SSE event received for this
   * session, or `null` when no event has arrived (or SSE is not
   * connected). When set and within `SSE_ACTIVE_MS`, the displayed
   * `optimisticStatus` is upgraded from `waiting` to `busy`.
   */
  lastSseEventAt?: number | null;
}

export interface UseSessionStatusResult {
  /** Status without the debounce — flickers on tool-call boundaries. */
  rawOptimisticStatus: Session['status'];
  /**
   * Status displayed in the badge. Identical to `rawOptimisticStatus`
   * except that `busy → waiting` transitions are held for
   * STATUS_GRACE_MS so quick tool-call gaps don't flash a "waiting"
   * pulse before the next turn starts.
   */
  optimisticStatus: Session['status'];
  /**
   * Output tokens per second, scoped to the current run window.
   * `null` when the assistant isn't running, when there isn't enough
   * data to estimate (< 100 ms or zero output), or when a permission
   * / question prompt is pending (user think time would deflate the
   * rate).
   */
  liveTokensPerSecond: number | null;
}

/**
 * Owns the optimistic-status debouncer and the live tokens-per-
 * second indicator for the page. Both signals are derived from the
 * messages array plus the subagent token snapshot; isolating them
 * here keeps the SessionDetail composition root free of timer
 * plumbing.
 *
 * The hook expects useSubagentTracking's `setSubagentTokens` so it
 * can clear the map when a run ends — a fresh run shouldn't carry
 * subagent tokens from the previous one into its TPS window.
 */
export function useSessionStatus({
  lastMsg,
  messages,
  subagentTokens,
  setSubagentTokens,
  isRunning,
  pendingPermission,
  pendingQuestion,
  lastSseEventAt = null,
}: UseSessionStatusOptions): UseSessionStatusResult {
  trackRender('useSessionStatus', { isRunning, lastMsgId: lastMsg?.id });
  // Raw status derived from the last message — may flicker between
  // "busy" and "waiting" during tool-call turn boundaries when the
  // agent finishes one turn and immediately starts another.
  const rawOptimisticStatus = deriveRawStatus(lastMsg);

  // Debounced status: when transitioning from "busy" to "waiting",
  // hold "busy" for a grace period before committing. If the agent
  // starts a new turn within that window the "waiting" flash is
  // suppressed entirely. Transitions to "error", "done", or "busy"
  // are applied immediately.
  const [optimisticStatus, setOptimisticStatus] = useState<Session['status']>(rawOptimisticStatus);
  const optimisticStatusRef = useSyncRef(optimisticStatus);
  const statusGraceRef = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (statusGraceRef.current !== null) window.clearTimeout(statusGraceRef.current);
    };
  }, []);

  // The synchronous setOptimisticStatus call below is intentional:
  // when the raw status changes to anything other than the
  // busy→waiting transition, we want the badge to update on the
  // very next paint. Debouncing busy→waiting via the timeout
  // arm above is the only path that defers the update.
  //
  // IMPORTANT: `optimisticStatus` is read via ref instead of being
  // listed as a dependency. Listing it would create a self-referencing
  // cycle (the effect sets the value it depends on), doubling the
  // render count per status change and amplifying other re-render
  // cascades.
  useEffect(() => {
    const currentOptimistic = optimisticStatusRef.current;
    if (rawOptimisticStatus === currentOptimistic) {
      // Already in sync — clear any pending grace timer.
      if (statusGraceRef.current !== null) {
        window.clearTimeout(statusGraceRef.current);
        statusGraceRef.current = null;
      }
      return;
    }
    if (currentOptimistic === 'busy' && rawOptimisticStatus === 'waiting') {
      if (statusGraceRef.current !== null) return; // timer already running
      statusGraceRef.current = window.setTimeout(() => {
        statusGraceRef.current = null;
        setOptimisticStatus(rawOptimisticStatus);
      }, STATUS_GRACE_MS);
      return;
    }
    if (statusGraceRef.current !== null) {
      window.clearTimeout(statusGraceRef.current);
      statusGraceRef.current = null;
    }
    setOptimisticStatus(rawOptimisticStatus);
  // eslint-disable-next-line react-hooks/exhaustive-deps -- optimisticStatusRef is a stable ref; listing optimisticStatus here would create a self-referencing render cycle.
  }, [rawOptimisticStatus]);

  // SSE-freshness override: when an SSE event arrived within
  // SSE_ACTIVE_MS, treat the session as actively producing output
  // even if the last message in our snapshot has finished. We hold a
  // boolean in state (rather than recomputing from `Date.now()` on
  // every render) so the override flips off via a real React update
  // when the window expires, even when no other props change.
  const [sseActive, setSseActive] = useState(false);
  useEffect(() => {
    if (lastSseEventAt === null) {
      if (sseActive) setSseActive(false);
      return;
    }
    const elapsed = Date.now() - lastSseEventAt;
    const remaining = SSE_ACTIVE_MS - elapsed;
    if (remaining <= 0) {
      if (sseActive) setSseActive(false);
      return;
    }
    if (!sseActive) setSseActive(true);
    const handle = window.setTimeout(() => setSseActive(false), remaining);
    return () => window.clearTimeout(handle);
  }, [lastSseEventAt, sseActive]);

  // Final displayed status: SSE freshness upgrades `waiting` to
  // `busy` (terminal states `error` / `done` are not overridden).
  const displayedOptimisticStatus: Session['status'] =
    sseActive && optimisticStatus === 'waiting' ? 'busy' : optimisticStatus;

  // Live tokens-per-second: sum output tokens across all assistant
  // messages in the current run window (since the last user message)
  // plus tokens from subagent sessions, divided by the sum of
  // per-message LLM durations.
  //
  // Per-message durations exclude idle time between messages (tool
  // execution, etc.). When a permission or question prompt is
  // pending, in-flight messages are excluded entirely since the LLM
  // isn't generating — this prevents user think time from deflating
  // TPS.
  //
  // The math is intentionally cumulative (totalOutput / totalDuration
  // over the run window). What we throttle here is the *UI update
  // rate*: every input — `messages`, `subagentTokens`, the prompt
  // refs, *and* the `setSubagentTokens` setter — flows through a ref
  // so the polling effect's dependency list only contains
  // `[isRunning]`. Without the setter ref, every parent render
  // (which happens many times per second during streaming because
  // `setSubagentTokens` from useSubagentTracking is recreated on
  // each render) would tear down + re-arm the interval and re-run
  // `computeTps` synchronously, defeating the 1 Hz budget. With it,
  // the interval is created once when a run starts and runs strictly
  // at 1 Hz until the run ends.
  const [liveTokensPerSecond, setLiveTokensPerSecond] = useState<number | null>(null);
  const messagesRef = useSyncRef(messages);
  const subagentTokensRef = useSyncRef(subagentTokens);
  const pendingPermissionRef = useSyncRef(pendingPermission);
  const pendingQuestionRef = useSyncRef(pendingQuestion);
  const setSubagentTokensRef = useSyncRef(setSubagentTokens);

  useEffect(() => {
    if (!isRunning) {
      setLiveTokensPerSecond(null);
      // Clear subagent token tracking when the run ends so the next
      // run starts fresh.
      setSubagentTokensRef.current((prev) => (prev.size > 0 ? new Map() : prev));
      return;
    }
    const computeTps = () => {
      const messages = messagesRef.current;
      const subagentTokens = subagentTokensRef.current;
      const promptPending =
        pendingPermissionRef.current !== null || pendingQuestionRef.current !== null;
      // Find the start of the current run window: the index after
      // the last user message.
      let windowStart = 0;
      for (let i = messages.length - 1; i >= 0; i--) {
        if (messages[i].data?.role === 'user') { windowStart = i + 1; break; }
      }
      let totalOutput = 0;
      let totalDurationMs = 0;
      const now = Date.now();
      for (let i = windowStart; i < messages.length; i++) {
        const m = messages[i];
        if (m.data?.role !== 'assistant') continue;
        const created = m.data.time?.created;
        if (!created) continue;
        const output = m.data.tokens?.output || 0;
        const completed = m.data.time?.completed;
        const isInFlight = !completed || completed <= created;
        // Skip in-flight messages entirely when a prompt is pending.
        if (isInFlight && promptPending) continue;
        const endTime = isInFlight ? now : completed;
        const durationMs = endTime - created;
        if (durationMs <= 0) continue;
        totalOutput += output;
        totalDurationMs += durationMs;
      }
      // Include output tokens and durations from subagent sessions
      // (captured via SSE). Subagent entries only track `created`,
      // so we always treat them as in-flight and use `now - created`
      // for the duration.
      for (const entry of subagentTokens.values()) {
        const durationMs = now - entry.created;
        if (durationMs <= 0) continue;
        totalOutput += entry.output;
        totalDurationMs += durationMs;
      }
      if (totalOutput > 0 && totalDurationMs > 100) {
        setLiveTokensPerSecond(totalOutput / (totalDurationMs / 1000));
        return;
      }
      setLiveTokensPerSecond(null);
    };
    computeTps();
    const interval = setInterval(computeTps, 1000);
    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- inputs are read via refs to keep the update rate at 1 Hz; ref objects are stable, and isRunning is the only signal that should re-arm the interval.
  }, [isRunning]);

  return {
    rawOptimisticStatus,
    optimisticStatus: displayedOptimisticStatus,
    liveTokensPerSecond,
  };
}
