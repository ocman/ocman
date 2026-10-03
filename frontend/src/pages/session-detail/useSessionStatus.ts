import { useEffect } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { Message, Part, Session, TaskSessionData } from '../../lib/api';
import type { PendingPermission } from '../../lib/sseHelpers';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';
import type { SubagentTokenMap } from './useSubagentTracking';
import { trackRender } from '../../lib/renderRateMonitor';
import { estimateThroughput } from '../../lib/throughput';

export interface UseSessionStatusOptions {
  /** Most recent message in the page's messages array (or null). */
  lastMsg: Message | null;
  /** Whole messages array — used to scope the TPS window to the
   *  current run (since the last user turn). */
  messages: Message[];
  parts?: Part[];
  taskLiveOutput?: Record<string, TaskSessionData>;
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
  displayStatus: Session['status'];
  /**
   * Estimated output tokens per model-request second for completed
   * samples in this run, excluding recorded tool/approval waits.
   * Null when not running or no trustworthy sample is available.
   */
  liveTokensPerSecond: number | null;
}

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
  parts = [],
  taskLiveOutput,
  setSubagentTokens,
  sessionStatus = null,
  awaitingAssistantResponse = false,
  isRunning,
}: UseSessionStatusOptions): UseSessionStatusResult {
  trackRender('useSessionStatus', { isRunning, lastMsgId: lastMsg?.id });
  const justSentPrompt = awaitingAssistantResponse && lastMsg?.data?.role === 'user';
  const lastMsgErrored = lastMsg?.data?.finish === 'error' || !!lastMsg?.data?.error;
  const displayStatus = resolveDisplayStatus(sessionStatus, justSentPrompt, lastMsgErrored);

  const liveTokensPerSecond = isRunning ? estimateThroughput(messages, parts, taskLiveOutput) : null;
  useEffect(() => {
    if (!isRunning) {
      setSubagentTokens((prev) => (prev.size > 0 ? new Map() : prev));
    }
  }, [isRunning, setSubagentTokens]);

  return {
    displayStatus,
    liveTokensPerSecond,
  };
}
