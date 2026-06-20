import { createContext, useContext } from 'react';
import type { Message, Part } from './api';

/**
 * Aggregated stats for a single "turn" — one user prompt plus all the
 * assistant messages that replied to it.
 *
 * The same aggregate is mapped against *every* assistant message in the
 * turn, but exactly one message — the current last assistant message —
 * is marked as the `isSummaryAnchor`. Only the anchor renders the
 * summary bar.
 *
 * Mapping the aggregate to all turn messages (not just the last one) is
 * what keeps the turn line from flickering while the agent streams:
 * during a multi-step turn (e.g. text → tool call → text) OpenCode keeps
 * appending new assistant messages, so the "last" message — and thus the
 * anchor — changes from one render to the next. Because the aggregate is
 * recomputed in a single pass, the previously-anchored message still has
 * a valid (non-anchor) entry while the new anchor already carries the
 * full aggregate, so there is never a frame where no message owns the
 * turn line.
 *
 * All counts are best-effort while the turn is in progress:
 * - Token/cost fields include any completed assistant messages in the turn.
 * - `isLive` is true when the last assistant message has no finish reason
 *   (still streaming), so the bar can show a "live" indicator.
 */
export interface TurnAggregate {
  /** Unix ms from user message creation to last assistant message completion. */
  wallClockMs: number | null;
  /** Sum of output tokens across all assistant messages in the turn. */
  tokensOut: number;
  /** Sum of input tokens across all assistant messages in the turn. */
  tokensIn: number;
  /** Sum of USD cost across all assistant messages in the turn. */
  cost: number;
  /** Number of tool-call parts across all assistant messages in the turn. */
  toolCalls: number;
  /** Average output tok/s across LLM calls in the turn that have timing. */
  tps: number | null;
  /** True when the last assistant message has no finish reason yet. */
  isLive: boolean;
  /** Unix ms when the user message was sent (turn start). */
  startedAt: number;
  /**
   * True only for the single assistant message that should render the
   * turn summary bar (the current last assistant message of the turn).
   * Non-anchor messages still carry the aggregate so the line never
   * blanks out while ownership moves between messages mid-turn.
   */
  isSummaryAnchor: boolean;
}

/** Map from "assistant message id" → TurnAggregate for its turn. */
export type TurnStatsMap = Map<string, TurnAggregate>;

/**
 * Compute per-turn aggregates from the flat messages + parts arrays.
 *
 * Turn boundaries are identified by user-message role transitions: a new
 * turn starts at each user message. All assistant messages between two
 * consecutive user messages (or from the last user message to end of list)
 * belong to the same turn. The last assistant message in that group carries
 * the aggregated data.
 */
export function computeTurnStats(messages: Message[], parts: Part[]): TurnStatsMap {
  const map: TurnStatsMap = new Map();

  // Build a quick index: messageId → count of tool parts
  const toolCountByMsg: Record<string, number> = {};
  for (const p of parts) {
    const data = typeof p.data === 'string' ? tryParse(p.data) : p.data;
    if (data && typeof data === 'object' && 'type' in data && data.type === 'tool') {
      toolCountByMsg[p.messageId] = (toolCountByMsg[p.messageId] ?? 0) + 1;
    }
  }

  const filtered = messages.filter(
    (m) => m.data?.role === 'user' || m.data?.role === 'assistant',
  );

  // Group into turns: [ { userMsg, assistantMsgs[] }, ... ]
  const turns: Array<{ userMsg: Message; assistantMsgs: Message[] }> = [];
  let current: { userMsg: Message; assistantMsgs: Message[] } | null = null;

  for (const m of filtered) {
    if (m.data.role === 'user') {
      current = { userMsg: m, assistantMsgs: [] };
      turns.push(current);
    } else if (m.data.role === 'assistant' && current) {
      current.assistantMsgs.push(m);
    }
  }

  for (const { userMsg, assistantMsgs } of turns) {
    if (assistantMsgs.length === 0) continue;

    const lastAsst = assistantMsgs[assistantMsgs.length - 1];
    let tokensOut = 0;
    let tokensIn = 0;
    let cost = 0;
    let toolCalls = 0;
    let totalTpsNumerator = 0;
    let totalTpsDenominator = 0;

    for (const a of assistantMsgs) {
      tokensOut += a.data.tokens?.output ?? 0;
      tokensIn += a.data.tokens?.input ?? 0;
      cost += a.data.cost ?? 0;
      toolCalls += toolCountByMsg[a.id] ?? 0;

      const t = a.data.time;
      const out = a.data.tokens?.output;
      if (t?.created && t?.completed && out) {
        const d = (t.completed - t.created) / 1000;
        if (d > 0) {
          totalTpsNumerator += out;
          totalTpsDenominator += d;
        }
      }
    }

    const tps =
      totalTpsDenominator > 0 ? totalTpsNumerator / totalTpsDenominator : null;

    // Wall-clock end: use the message row's timeCreated (when the DB row was
    // written) rather than data.time.completed (which only covers the final
    // LLM call, not tool-execution time between calls). Fall back to null
    // while the turn is still live.
    const wallClockEnd =
      lastAsst.data.finish || lastAsst.data.error ? lastAsst.timeCreated : null;
    const wallClockMs =
      wallClockEnd !== null ? wallClockEnd - userMsg.timeCreated : null;

    const isLive = !lastAsst.data.finish && !lastAsst.data.error;

    // Map the same aggregate to every assistant message in the turn so
    // the turn line never blanks out while ownership moves between
    // messages mid-turn (e.g. during tool calls). Only the last message
    // is the anchor that actually renders the bar.
    for (const a of assistantMsgs) {
      map.set(a.id, {
        wallClockMs,
        tokensOut,
        tokensIn,
        cost,
        toolCalls,
        tps,
        isLive,
        startedAt: userMsg.timeCreated,
        isSummaryAnchor: a.id === lastAsst.id,
      });
    }
  }

  return map;
}

function tryParse(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return null;
  }
}

export const TurnStatsContext = createContext<TurnStatsMap>(new Map());

export function useTurnStats(messageId: string): TurnAggregate | undefined {
  return useContext(TurnStatsContext).get(messageId);
}
