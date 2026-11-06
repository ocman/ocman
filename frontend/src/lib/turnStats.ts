import { createContext, useContext } from 'react';
import { useAuiState } from '@assistant-ui/react';
import type { Message, Part, PartData } from './api';
import { formatModelRef } from './sessionStatus';
import { throughputSample } from './throughput';
import { isTurnNotification } from './turnNotification';
import { parsePart } from './parsePart';

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
 * - `isLive` follows the session lifecycle when supplied, falling back to
 *   message completion metadata for views without a live session status.
 */
export interface TurnAggregate {
  /** Ms from user message creation to last assistant message completion. */
  wallClockMs: number | null;
  /** Sum of output tokens across all assistant messages in the turn. */
  tokensOut: number;
  /** Sum of input tokens across all assistant messages in the turn. */
  tokensIn: number;
  /** Sum of USD cost across all assistant messages in the turn. */
  cost: number;
  /** Number of tool-call parts across all assistant messages in the turn. */
  toolCalls: number;
  /** Estimated output tok/s, excluding recorded tool and approval waits. */
  tps: number | null;
  /** True while the turn is active. */
  isLive: boolean;
  /** True when this completed turn likely rebuilt a previously used prompt cache. */
  promptCacheRebuilt: boolean;
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

interface CachedTurn {
  assistants: Message[];
  parts: Part[];
  running: boolean | undefined;
  hadCacheRead: boolean;
  hasCompletedCacheRead: boolean;
  entries: TurnStatsMap;
}

/** One cache per conversation; retain only turns in the current snapshot. */
export function createComputeTurnStats(): typeof computeTurnStats {
  let turns = new Map<Message, CachedTurn>();
  let previous: TurnStatsMap = new Map();
  return (messages, parts, isRunning) => {
    const next = new Map<Message, CachedTurn>();
    const result = computeTurnStats(messages, parts, isRunning, turns, next);
    turns = next;
    if (result.size === previous.size && [...result].every(([id, stats]) => previous.get(id) === stats)) return previous;
    previous = result;
    return result;
  };
}

/**
 * Compute per-turn aggregates from the flat messages + parts arrays.
 *
 * Turn boundaries are identified by real user prompts, not injected
 * user-role notifications. All assistant messages between two
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
  isRunning?: boolean,
  cache?: Map<Message, CachedTurn>,
  nextCache?: Map<Message, CachedTurn>,
): TurnStatsMap {
  const map: TurnStatsMap = new Map();

  // Build a quick index: messageId → count of tool parts
  const toolCountByMsg: Record<string, number> = {};
  const toolsByMsg: Record<string, PartData[]> = {};
  const partsByMsg: Record<string, PartData[]> = {};
  const rawPartsByMsg: Record<string, Part[]> = {};
  for (const p of parts) {
    (rawPartsByMsg[p.messageId] ??= []).push(p);
    const data = parsePart(p);
    if (data && typeof data === 'object' && 'type' in data) {
      (partsByMsg[p.messageId] ??= []).push(data as PartData);
    }
    if (data && typeof data === 'object' && 'type' in data && data.type === 'tool') {
      toolCountByMsg[p.messageId] = (toolCountByMsg[p.messageId] ?? 0) + 1;
      (toolsByMsg[p.messageId] ??= []).push(data as PartData);
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
      if (isTurnNotification(m, partsByMsg[m.id] ?? [])) continue;
      current = { userMsg: m, assistantMsgs: [] };
      turns.push(current);
    } else if (m.data.role === 'assistant' && current) {
      current.assistantMsgs.push(m);
    }
  }

  const lastTurn = turns[turns.length - 1];
  let hadCompletedCacheRead = false;

  for (const turn of turns) {
    const { userMsg, assistantMsgs } = turn;
    if (assistantMsgs.length === 0) continue;

    const isLastTurn = turn === lastTurn;
    const running = isLastTurn ? isRunning : undefined;
    const turnParts = assistantMsgs.flatMap((m) => rawPartsByMsg[m.id] ?? []);
    const cached = cache?.get(userMsg);
    if (cached && cached.running === running && cached.hadCacheRead === hadCompletedCacheRead
      && cached.assistants.length === assistantMsgs.length && cached.assistants.every((m, i) => m === assistantMsgs[i])
      && cached.parts.length === turnParts.length && cached.parts.every((p, i) => p === turnParts[i])) {
      for (const [id, stats] of cached.entries) map.set(id, stats);
      nextCache?.set(userMsg, cached);
      hadCompletedCacheRead = cached.hasCompletedCacheRead;
      continue;
    }
    const lastAsst = assistantMsgs[assistantMsgs.length - 1];
    let tokensOut = 0;
    let tokensIn = 0;
    let cost = 0;
    let toolCalls = 0;
    let totalTpsNumerator = 0;
    let totalTpsDenominator = 0;
    let model = '';
    let cacheRead = 0;
    let cacheWrite = 0;
    let hasCacheMetrics = false;

    for (const a of assistantMsgs) {
      tokensOut += a.data.tokens?.output ?? 0;
      tokensIn += a.data.tokens?.input ?? 0;
      cost += a.data.cost ?? 0;
      toolCalls += toolCountByMsg[a.id] ?? 0;
      const cache = a.data.tokens?.cache;
      if (cache?.read !== undefined || cache?.write !== undefined) hasCacheMetrics = true;
      cacheRead += cache?.read ?? 0;
      cacheWrite += cache?.write ?? 0;
      // Keep the most recent model seen in the turn so the summary bar
      // reflects what actually produced the final reply.
      const ref = messageModelRef(a);
      if (ref) model = ref;

      const [out, duration] = throughputSample(a, toolsByMsg[a.id] ?? []);
      totalTpsNumerator += out;
      totalTpsDenominator += duration / 1000;
    }

    const tps =
      totalTpsDenominator > 0 ? totalTpsNumerator / totalTpsDenominator : null;

    // The latest turn follows the composer's running state. A completed
    // message can be an intermediate step while the session is still busy.
    // Without a running state, fall back to the trailing message's lifecycle.
    const isCompleted = lastAsst.data.time?.completed !== undefined;
    const isLive = isLastTurn && isRunning !== undefined
      ? isRunning
      : !lastAsst.data.error && !isCompleted && !lastAsst.data.finish;

    // Wall-clock end: when the turn's final reply completed. Measured from
    // the user message, so tool time between calls is included. Falls back
    // to the reply's creation when no completion was recorded, and to null
    // while the turn is still live.
    const wallClockEnd = isLive ? null : lastAsst.data.time?.completed ?? lastAsst.timeCreated;
    const wallClockMs =
      wallClockEnd !== null ? wallClockEnd - userMsg.timeCreated : null;
    const promptCacheRebuilt = !isLive && hadCompletedCacheRead
      && hasCacheMetrics && cacheRead === 0 && cacheWrite > 0;

    // Map the same aggregate to every assistant message in the turn so
    // the turn line never blanks out while ownership moves between
    // messages mid-turn (e.g. during tool calls). Only the last message
    // is the anchor that actually renders the bar.
    const entries: TurnStatsMap = new Map();
    for (const a of assistantMsgs) {
      const stats: TurnAggregate = {
        wallClockMs,
        tokensOut,
        tokensIn,
        cost,
        toolCalls,
        tps,
        isLive,
        promptCacheRebuilt,
        startedAt: userMsg.timeCreated,
        model,
        isSummaryAnchor: a.id === lastAsst.id,
      };
      // Text-only deltas may change dependencies without changing accounting.
      const prior = cached?.entries.get(a.id);
      const stable = prior && (Object.keys(stats) as Array<keyof TurnAggregate>).every((key) => Object.is(stats[key], prior[key])) ? prior : stats;
      map.set(a.id, stable);
      entries.set(a.id, stable);
    }
    const hadCacheRead = hadCompletedCacheRead;
    if (!isLive && cacheRead > 0) hadCompletedCacheRead = true;
    nextCache?.set(userMsg, { assistants: assistantMsgs, parts: turnParts, running, hadCacheRead, hasCompletedCacheRead: hadCompletedCacheRead, entries });
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

export function useTurnStats(messageId?: string): TurnAggregate | undefined {
  return useAuiState((state) => {
    const message = messageId === undefined ? state.message : state.thread.messages.find((m) => m.id === messageId);
    return message?.metadata.custom?.turnStats as TurnAggregate | undefined;
  });
}

export function useModelLabel(model: string): string {
  return useContext(ModelLabelsContext)[model] || humanizeModelRef(model);
}
