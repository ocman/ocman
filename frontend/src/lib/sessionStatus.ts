import type { AgentInfo, Message, Session } from './api';

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
 * Derive an optimistic raw status from the most recent message.
 * Mirrors the server-side derivation in `internal/db/types.go` so
 * the next poll confirms (rather than corrects) the optimistic
 * value.
 */
export function deriveRawStatus(lastMsg: Message | null): Session['status'] {
  if (!lastMsg) return 'done';
  const data = lastMsg.data;
  if (data?.role !== 'assistant') return 'done';
  if (data?.finish === 'error' || data?.error) return 'error';
  if (data?.finish) return 'waiting';
  return 'busy';
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
  if (sessionStatus === 'busy') return true;
  if (sessionStatus === 'error' || sessionStatus === 'done') return false;
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
