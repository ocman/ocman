import { useApiStore } from './apiStore';
import type { SessionChanges } from './api';
import {
  useSessionResourceQuery,
  type SessionResourceOptions,
  type SessionResourceResult,
} from './useSessionResourceQuery';

// Stable empty payload used when the hook is disabled or the platform
// doesn't support file-change aggregation. Stable identity so
// re-renders don't churn referential equality.
const EMPTY_CHANGES: SessionChanges = {
  sessionId: '',
  supported: false,
  totalAdditions: 0,
  totalDeletions: 0,
  filesChanged: 0,
  files: [],
};

export type UseSessionChangesOptions = SessionResourceOptions;
export type UseSessionChangesResult = SessionResourceResult<SessionChanges>;

/**
 * useSessionChanges fetches /api/session/{id}/changes and re-fetches
 * (coalesced) whenever `options.dirtyTick` changes.
 *
 * When `enabled` is false, returns a synchronous EMPTY_CHANGES with
 * `loading: false` so the caller can render a "Not supported" empty
 * state without a flash of skeleton.
 */
export function useSessionChanges(
  sessionId: string | undefined,
  options: UseSessionChangesOptions = {},
): UseSessionChangesResult {
  const getSessionChanges = useApiStore((s) => s.getSessionChanges);
  return useSessionResourceQuery(
    sessionId ? ['session-changes', sessionId] : undefined,
    (signal) => getSessionChanges(sessionId!, signal),
    EMPTY_CHANGES,
    'Failed to load changes',
    options,
  );
}
