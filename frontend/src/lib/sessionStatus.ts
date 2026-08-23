import type { AgentInfo, Message, Session, SessionStatus } from './api';

/**
 * Aggregate token / cost counts derived from a single page of message
 * history. Used to keep the per-session header up to date from SSE
 * events without round-tripping to the server.
 */
export interface LiveTokens {
  tokensIn: number;
  tokensOut: number;
  tokensReasoning: number;
  cacheRead: number;
  cacheWrite: number;
  totalCost: number;
}

/**
 * Display-ready combination of server-side totals and locally
 * computed live tokens. The server values cover paginated-out
 * messages; the live values pick up incremental SSE updates before
 * the next reload.
 */
export interface TokenStats {
  input: number;
  output: number;
  reasoning: number;
  cacheRead: number;
  cacheWrite: number;
  totalCost: number;
  contextWindow?: number;
}

export interface SessionTreeStats {
  input: number;
  output: number;
  totalCost: number;
  totalEstCost: number;
  totalEffectiveCost: number;
  sessions: number;
}

/**
 * Convenience union: a session-shaped value that may carry the
 * page-only fields (`contextTokenCount`, `defaultAgent`,
 * `defaultModel`) returned from `/api/session/{id}`.
 */
export type SessionWithDefaults = Session & {
  contextTokenCount?: number;
  defaultAgent?: string;
  defaultModel?: string;
};

/**
 * Format a `provider/model` reference, falling back to the model
 * alone when no provider is set, and to an empty string when no
 * model is set.
 */
export function formatModelRef(providerId?: string, modelId?: string): string {
  if (!modelId) return '';
  return providerId ? `${providerId}/${modelId}` : modelId;
}

/**
 * Extract a `provider/model` reference from an agent definition, if it
 * declares one. Agents may carry `model` either as a bare
 * `"provider/model"` string or as a `{ providerID, modelID }` object.
 * Returns '' when the agent declares no model.
 */
export function agentModelRef(agent: AgentInfo | undefined): string {
  const model = agent?.model;
  if (!model) return '';
  if (typeof model === 'string') return model.trim();
  return formatModelRef(model.providerID, model.modelID);
}

/**
 * True for statuses that mean no turn is running. Anything but `busy`
 * qualifies, including `interrupted` — the turn is over, it just didn't
 * reach a conclusion. Callers use it to grey out a dot the user has
 * already seen; keeping the check in one place is what stops a newly
 * added status from silently missing a branch.
 */
export function isTerminalStatus(status: SessionStatus | null | undefined): boolean {
  return status !== null && status !== undefined && status !== 'busy';
}

function isCompletedToolOnlyAssistant(message: Message | null): boolean {
  const data = message?.data;
  return data?.role === 'assistant' && !data.finish && !data.error && data.time?.completed !== undefined;
}

/**
 * The assistant is "running" whenever the last message is from the
 * assistant with no `finish` reason and no `error` (still streaming).
 *
 * A trailing user message does NOT count as running — it may be a
 * manual tool execution (e.g. `git stash`) that doesn't trigger an
 * LLM call. This aligns with the server-side `InferSessionStatus`
 * which returns "done" for user messages. The brief gap between
 * sending a prompt and the first SSE assistant chunk is preferable
 * to a false "working" indicator that persists indefinitely after
 * tool executions.
 */
export function isSessionRunning(
  lastMsg: Message | null,
  sessionStatus?: Session['status'] | null,
  awaitingAssistantResponse = false,
): boolean {
  if (awaitingAssistantResponse && lastMsg?.data?.role === 'user') return true;
  // A direct !bash command is stored as an assistant envelope without a
  // finish reason, but its completion timestamp proves no turn is active.
  if (isCompletedToolOnlyAssistant(lastMsg)) return false;
  if (sessionStatus === 'busy') return true;
  if (sessionStatus === 'error' || sessionStatus === 'done' || sessionStatus === 'interrupted') return false;
  if (!lastMsg) return false;
  const data = lastMsg.data;
  if (data?.role === 'assistant' && !data?.finish && !data?.error) return true;
  return false;
}

/**
 * Sum tokens and cost across the assistant messages in a session
 * page. Non-assistant messages and assistant messages without a
 * `tokens` block contribute nothing.
 */
export function computeLiveTokens(messages: Message[]): LiveTokens {
  let tokensIn = 0;
  let tokensOut = 0;
  let tokensReasoning = 0;
  let cacheRead = 0;
  let cacheWrite = 0;
  let totalCost = 0;
  for (const m of messages) {
    if (m.data?.role !== 'assistant') continue;
    const tokens = m.data.tokens;
    if (tokens) {
      tokensIn += tokens.input || 0;
      tokensOut += tokens.output || 0;
      tokensReasoning += tokens.reasoning || 0;
      cacheRead += tokens.cache?.read || 0;
      cacheWrite += tokens.cache?.write || 0;
    }
    if (m.data.cost) totalCost += m.data.cost;
  }
  return { tokensIn, tokensOut, tokensReasoning, cacheRead, cacheWrite, totalCost };
}

/**
 * Combine server-reported totals with locally-computed live tokens,
 * picking the larger value for input/output/cost so the display
 * never regresses while a turn is in progress and so the cost shows
 * up immediately when the session page renders before the message
 * list has streamed in (the live cost is only populated as
 * assistant messages arrive in the `messages` array).
 */
export function mergeTokenStats(
  session: SessionWithDefaults | null,
  liveTokens: LiveTokens,
): TokenStats {
  const displayTokensIn = Math.max(session?.totalInputTokens || 0, liveTokens.tokensIn);
  const displayTokensOut = Math.max(session?.totalOutputTokens || 0, liveTokens.tokensOut);
  const displayCost = Math.max(session?.totalCost || 0, liveTokens.totalCost);
  return {
    input: displayTokensIn,
    output: displayTokensOut,
    reasoning: liveTokens.tokensReasoning,
    cacheRead: liveTokens.cacheRead,
    cacheWrite: liveTokens.cacheWrite,
    totalCost: displayCost,
    contextWindow: session?.contextTokenCount,
  };
}

/** Sum the root's live totals with every known descendant session. */
export function aggregateSessionTreeStats(
  root: Session,
  sessions: Session[],
  rootStats: TokenStats,
): SessionTreeStats {
  const children = new Map<string, Session[]>();
  for (const session of sessions) {
    if (session.platform !== root.platform || !session.parentId) continue;
    const siblings = children.get(session.parentId) || [];
    siblings.push(session);
    children.set(session.parentId, siblings);
  }

  const treeRoot = sessions.find((session) => session.id === root.id && session.platform === root.platform);
  const totals = {
    input: rootStats.input,
    output: rootStats.output,
    totalCost: rootStats.totalCost,
    totalEstCost: treeRoot?.totalEstCost || 0,
    totalEffectiveCost: treeRoot?.totalEffectiveCost || rootStats.totalCost,
    sessions: 1,
  };
  const seen = new Set([root.id]);
  const pending = [...(children.get(root.id) || [])];
  while (pending.length > 0) {
    const session = pending.pop()!;
    if (seen.has(session.id)) continue;
    seen.add(session.id);
    totals.input += session.totalInputTokens || 0;
    totals.output += session.totalOutputTokens || 0;
    totals.totalCost += session.totalCost || 0;
    totals.totalEstCost += session.totalEstCost || 0;
    totals.totalEffectiveCost += session.totalEffectiveCost || session.totalCost || 0;
    totals.sessions += 1;
    pending.push(...(children.get(session.id) || []));
  }
  return totals;
}

/**
 * Derive the active model + agent for the composer footer by
 * scanning the message history newest-first. Falls back to the
 * session's defaults, then to empty strings.
 *
 * For `activeModel` we only consider assistant messages that are the
 * *direct primary response* to a user message — i.e. the first
 * assistant message that follows the most recent user turn. This
 * prevents skills or tool sub-calls (which may run with a different
 * model) from changing the displayed active model.
 */
export function deriveActiveModelAndAgent(
  messages: Message[],
  session: SessionWithDefaults | null,
): { activeModel: string; activeAgent: string } {
  let activeModel = '';
  let activeAgent = '';

  // Walk newest-first to find the most recent user message, then
  // take the model from the very next assistant message after it.
  // That assistant message is the primary response; anything before
  // the user message (i.e. earlier in the turn or from tool calls)
  // is ignored for model purposes.
  let seenUserMessage = false;
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    const role = m.data?.role;

    if (role === 'user') {
      seenUserMessage = true;
      continue;
    }

    if (role === 'assistant') {
      // Only pick up the model from assistant messages that directly
      // follow a user message (primary response in a turn).
      if (!activeModel && seenUserMessage) {
        const ref = formatModelRef(m.data?.providerID, m.data?.modelID);
        if (ref) activeModel = ref;
      }
      if (!activeAgent && m.data?.agent) {
        activeAgent = m.data.agent;
      }
    }

    if (activeModel && activeAgent) break;
  }

  if (!activeModel) activeModel = session?.defaultModel || '';
  if (!activeAgent) activeAgent = session?.defaultAgent || '';
  return { activeModel, activeAgent };
}
