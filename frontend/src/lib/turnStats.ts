import { createContext, useContext } from 'react';
import type { Message, Part } from './api';
import { formatModelRef } from './sessionStatus';

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
   * Raw `provider/model` reference for the turn, taken from the last
   * assistant message that carries model metadata. Empty string when no
   * assistant message in the turn reported a model.
   */
  model: string;
  /**
   * True only for the single assistant message that should render the
   * turn summary bar (the current last assistant message of the turn).
   * Non-anchor messages still carry the aggregate so the line never
   * blanks out while ownership moves between messages mid-turn.
   */
  isSummaryAnchor: boolean;
}

/**
 * Extract the raw `provider/model` reference from a message's data,
 * preferring the top-level `providerID`/`modelID` fields and falling
 * back to the nested `model` object some payloads use.
 */
export function messageModelRef(m: Message): string {
  const data = m.data as {
    providerID?: string;
    modelID?: string;
    model?: { providerID?: string; modelID?: string };
  } | undefined;
  if (!data) return '';
  const direct = formatModelRef(data.providerID, data.modelID);
  if (direct) return direct;
  return formatModelRef(data.model?.providerID, data.model?.modelID);
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
 *
 * `isRunning` is the session-level "agent is working" signal (see
 * `computeIsRunning`). It is required to keep the *last* turn marked live
 * across tool-call steps: OpenCode finishes each intermediate LLM step
 * with `finish: "tool-calls"`, which is indistinguishable from a turn
 * that legitimately ends on `tool-calls`. Per-message `finish` therefore
 * cannot tell "mid-turn tool call" from "turn done" — so while the
 * session is running, the last turn stays live regardless of the trailing
 * message's finish reason. This stops the turn line from flickering off
 * during read/bash/edit tool execution.
 */
export function computeTurnStats(
  messages: Message[],
  parts: Part[],
  isRunning = false,
): TurnStatsMap {
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

  const lastTurn = turns[turns.length - 1];

  for (const turn of turns) {
    const { userMsg, assistantMsgs } = turn;
    if (assistantMsgs.length === 0) continue;

    const isLastTurn = turn === lastTurn;
    const lastAsst = assistantMsgs[assistantMsgs.length - 1];
    let tokensOut = 0;
    let tokensIn = 0;
    let cost = 0;
    let toolCalls = 0;
    let totalTpsNumerator = 0;
    let totalTpsDenominator = 0;
    let model = '';

    for (const a of assistantMsgs) {
      tokensOut += a.data.tokens?.output ?? 0;
      tokensIn += a.data.tokens?.input ?? 0;
      cost += a.data.cost ?? 0;
      toolCalls += toolCountByMsg[a.id] ?? 0;
      // Keep the most recent model seen in the turn so the summary bar
      // reflects what actually produced the final reply.
      const ref = messageModelRef(a);
      if (ref) model = ref;

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

    // The last turn stays live while the session is running, even if its
    // trailing message reports `finish: "tool-calls"` (an intermediate
    // tool step). Only an error or a non-running session ends it. Earlier
    // turns are live only when their trailing message has no finish yet.
    const isLive = lastAsst.data.error
      ? false
      : isLastTurn
        ? isRunning || !lastAsst.data.finish
        : !lastAsst.data.finish;

    // Wall-clock end: use the message row's timeCreated (when the DB row was
    // written) rather than data.time.completed (which only covers the final
    // LLM call, not tool-execution time between calls). Fall back to null
    // while the turn is still live.
    const wallClockEnd = isLive ? null : lastAsst.timeCreated;
    const wallClockMs =
      wallClockEnd !== null ? wallClockEnd - userMsg.timeCreated : null;

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
        model,
        isSummaryAnchor: a.id === lastAsst.id,
      });
    }
  }

  return map;
}

export function latestTurnModel(messages: Message[], turnStatsMap: TurnStatsMap): string {
  for (let i = messages.length - 1; i >= 0; i--) {
    const model = turnStatsMap.get(messages[i].id)?.model;
    if (model) return model;
  }
  return '';
}

function tryParse(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return null;
  }
}

export const TurnStatsContext = createContext<TurnStatsMap>(new Map());

export type ModelLabelMap = Record<string, string>;

export const ModelLabelsContext = createContext<ModelLabelMap>({});

function titleToken(token: string): string {
  const lower = token.toLowerCase();
  if (/^gpt\d*$/.test(lower) || /^o\d+$/.test(lower)) return lower.toUpperCase();
  return lower.charAt(0).toUpperCase() + lower.slice(1);
}

export function humanizeModelRef(model: string): string {
  const rawModel = model.includes('/') ? model.slice(model.indexOf('/') + 1) : model;
  if (!rawModel) return model;

  const tokens = rawModel.split(/[-_]+/).filter(Boolean);
  const words: string[] = [];
  for (let i = 0; i < tokens.length; i++) {
    if (/^\d+$/.test(tokens[i])) {
      const version = [tokens[i]];
      while (i + 1 < tokens.length && /^\d+$/.test(tokens[i + 1])) {
        version.push(tokens[i + 1]);
        i++;
      }
      words.push(version.join('.'));
    } else {
      words.push(titleToken(tokens[i]));
    }
  }
  return words.join(' ') || model;
}

export function useTurnStats(messageId: string): TurnAggregate | undefined {
  return useContext(TurnStatsContext).get(messageId);
}

export function useModelLabel(model: string): string {
  return useContext(ModelLabelsContext)[model] || humanizeModelRef(model);
}
