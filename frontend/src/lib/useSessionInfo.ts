import { api } from './api';
import type { SessionInfo } from './api';
import {
  useSessionResourceQuery,
  type SessionResourceOptions,
  type SessionResourceResult,
} from './useSessionResourceQuery';

// Stable empty payload returned when the hook is disabled or the
// platform doesn't expose this data. Reused across renders so
// referential equality holds; downstream effects keying off `data`
// won't churn.
const EMPTY_INFO: SessionInfo = {
  sessionId: '',
  supported: false,
  context: { tokens: 0, cost: 0, estCost: 0 },
  tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
  mcpServers: [],
  lspServers: [],
  messages: { user: 0, assistant: 0 },
  commits: [],
};

export type UseSessionInfoOptions = SessionResourceOptions & { platformId?: string };
export type UseSessionInfoResult = SessionResourceResult<SessionInfo>;

/**
 * useSessionInfo fetches /api/session/{id}/info and re-fetches
 * (coalesced) whenever `options.dirtyTick` changes.
 *
 * When `enabled` is false, returns a synchronous EMPTY_INFO with
 * `loading: false` so the caller can render a "Not supported" empty
 * state without a flash of skeleton.
 */
export function useSessionInfo(
  sessionId: string | undefined,
  { platformId, ...options }: UseSessionInfoOptions = {},
): UseSessionInfoResult {
  const getSessionInfo = api.sessionInfo;
  return useSessionResourceQuery(
    sessionId ? ['session-info', platformId ?? 'local', sessionId] : undefined,
    (signal) => getSessionInfo(sessionId!, signal, platformId),
    EMPTY_INFO,
    'Failed to load session info',
    options,
  );
}
