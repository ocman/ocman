import type { Message, Session } from './api';

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
 * picking the larger value for input/output so the display never
 * regresses while a turn is in progress. The server-side cost is
 * not included here because the live cost is derived from the same
 * messages and stays consistent.
 */
export function mergeTokenStats(
  session: SessionWithDefaults | null,
  liveTokens: LiveTokens,
): TokenStats {
  const displayTokensIn = Math.max(session?.totalInputTokens || 0, liveTokens.tokensIn);
  const displayTokensOut = Math.max(session?.totalOutputTokens || 0, liveTokens.tokensOut);
  return {
    input: displayTokensIn,
    output: displayTokensOut,
    reasoning: liveTokens.tokensReasoning,
    cacheRead: liveTokens.cacheRead,
    cacheWrite: liveTokens.cacheWrite,
    totalCost: liveTokens.totalCost,
    contextWindow: session?.contextTokenCount,
  };
}

/**
 * Derive the active model + agent for the composer footer by
 * scanning the message history newest-first. Falls back to the
 * session's defaults, then to empty strings.
 */
export function deriveActiveModelAndAgent(
  messages: Message[],
  session: SessionWithDefaults | null,
): { activeModel: string; activeAgent: string } {
  let activeModel = '';
  let activeAgent = '';
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (!activeModel) {
      const ref = formatModelRef(m.data?.providerID, m.data?.modelID);
      if (ref) activeModel = ref;
    }
    if (!activeAgent && m.data?.agent) {
      activeAgent = m.data.agent;
    }
    if (activeModel && activeAgent) break;
  }
  if (!activeModel) activeModel = session?.defaultModel || '';
  if (!activeAgent) activeAgent = session?.defaultAgent || '';
  return { activeModel, activeAgent };
}
