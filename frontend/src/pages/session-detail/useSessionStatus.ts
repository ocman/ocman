import { useEffect, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { Message, Session } from '../../lib/api';
import type { PendingPermission } from '../../lib/sseHelpers';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';
import type { SubagentTokenMap } from './useSubagentTracking';
import { trackRender } from '../../lib/renderRateMonitor';

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
  /** Latest session status mirrored from the API/SSE session object. */
  sessionStatus?: Session['status'] | null;
  /**
   * True after the user sends a new prompt and before the session has
   * produced its first assistant message for that turn. This covers
   * the gap where the last message is still the user's optimistic
   * bubble, but the model is already queued/running.
   */
  awaitingAssistantResponse?: boolean;
  /** Whether the assistant is currently producing output. */
  isRunning: boolean;
  /** Pending permission, when one is waiting on the user. */
  pendingPermission: PendingPermission | null;
  /** Pending question, when one is waiting on the user. */
  pendingQuestion: PendingQuestion | null;
}

export interface UseSessionStatusResult {
  /**
   * Status displayed in the badge: the backend's settled status, with
   * two display-only layers on top (see `resolveDisplayStatus`).
   *
   * There is no debounce and no local re-derivation: `sessionStatus` is
   * the agent's own turn state (see db.SettleSessionStatus), so it stays
   * `busy` across tool-call boundaries instead of flickering to
   * `waiting` between steps. A grace window used to hide that flicker;
   * it only added staleness once the underlying status stopped lagging.
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
/**
 * The status the badge shows. It mirrors db.SettleSessionStatus: a live
 * turn wins outright, and only once the turn is settled does message
 * shape decide *which* terminal state it settled into.
 *
 * `sessionStatus` is the backend's own answer and is reported verbatim
 * for every state it can express. Two layers sit on top:
 *
 *  - `justSentPrompt`: the prompt has been accepted but no assistant
 *    message exists for the turn yet, so the badge would otherwise sit
 *    on the previous turn's terminal state for a beat.
 *  - `lastMsgErrored`: OpenCode's `session.status` vocabulary is
 *    busy|retry|idle only (internal/platforms/opencode/live_status.go),
 *    so it never reports a failure. A failed turn arrives as an errored
 *    message followed by `session.idle` — which reduces to `done`.
 *    Without this arm the badge claims a failed turn succeeded until the
 *    REST reconcile round trip corrects it, and the active sidebar row
 *    (which overlays this value) downgrades the correct `error` every
 *    other row already shows.
 *
 * Both are display-only: neither is written back into the session or
 * into shared recent-session state.
 */
function resolveDisplayStatus(
  sessionStatus: Session['status'] | null | undefined,
  justSentPrompt: boolean,
  lastMsgErrored: boolean,
): Session['status'] {
  // A live turn outranks the tail: an errored message from the previous
  // turn must not mask the one that is running now.
  if (justSentPrompt || sessionStatus === 'busy') return 'busy';
  if (lastMsgErrored) return 'error';
  return sessionStatus ?? 'done';
}

export function useSessionStatus({
  lastMsg,
  messages,
  subagentTokens,
  setSubagentTokens,
  sessionStatus = null,
  awaitingAssistantResponse = false,
  isRunning,
  pendingPermission,
  pendingQuestion,
}: UseSessionStatusOptions): UseSessionStatusResult {
  trackRender('useSessionStatus', { isRunning, lastMsgId: lastMsg?.id });
  const justSentPrompt = awaitingAssistantResponse && lastMsg?.data?.role === 'user';
  const lastMsgErrored = lastMsg?.data?.finish === 'error' || !!lastMsg?.data?.error;
  const optimisticStatus = resolveDisplayStatus(sessionStatus, justSentPrompt, lastMsgErrored);

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
  const [liveTokensPerSecond, setLiveTokensPerSecond] = useState<number | null>(null);
  // The synchronous setLiveTokensPerSecond / setSubagentTokens
  // calls below are the cleanup arm: they fire once when
  // `isRunning` flips from true to false, not on every render.
  // The polling branch only writes through setLiveTokensPerSecond
  // inside `setInterval` callbacks (already deferred).
  //
  // The disable below is not new behaviour: this write was always here,
  // but the hook also read the clock during render, which made the
  // react-hooks rules bail out before reaching it. Removing that read
  // (the work-event window is gone) exposed the pre-existing report.
  useEffect(() => {
    if (!isRunning) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- run-end cleanup arm; fires on the isRunning true→false edge, not per render.
      setLiveTokensPerSecond(null);
      // Clear subagent token tracking when the run ends so the next
      // run starts fresh.
      setSubagentTokens((prev) => (prev.size > 0 ? new Map() : prev));
      return;
    }
    const computeTps = () => {
      // Find the start of the current run window: the index after
      // the last user message.
      let windowStart = 0;
      for (let i = messages.length - 1; i >= 0; i--) {
        if (messages[i].data?.role === 'user') { windowStart = i + 1; break; }
      }
      const promptPending = pendingPermission !== null || pendingQuestion !== null;
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
  }, [isRunning, messages, subagentTokens, setSubagentTokens, pendingPermission, pendingQuestion]);

  return {
    optimisticStatus,
    liveTokensPerSecond,
  };
}
