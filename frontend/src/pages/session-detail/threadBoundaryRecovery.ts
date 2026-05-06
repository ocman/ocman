const ASSISTANT_UI_LOOKUP_ERROR_PREFIX = 'tapClientLookup:';

/**
 * assistant-ui occasionally throws a transient index/key lookup error while
 * its external-store view catches up with a freshly refreshed message tree.
 * A session-detail reload typically fixes it immediately, so the UI boundary
 * can auto-retry this specific signature without masking unrelated crashes.
 *
 * Historical context: this used to fire more often when
 * `convertMessages` had a module-level singleton result-array cache
 * shared across sessions. Now that each `OcmanRuntimeProvider`
 * instance owns its own cache via `createConvertMessages()`, the
 * underlying inconsistency is far less likely. The recovery is kept
 * as a belt-and-braces safety net for genuine assistant-ui internal
 * hiccups.
 */
export function isRecoverableThreadBoundaryError(error: Error): boolean {
  return error.message.startsWith(ASSISTANT_UI_LOOKUP_ERROR_PREFIX);
}
