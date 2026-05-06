const ASSISTANT_UI_LOOKUP_ERROR_PREFIX = 'tapClientLookup:';

/**
 * assistant-ui occasionally throws a transient index/key lookup error while
 * its external-store view catches up with a freshly refreshed message tree.
 * A session-detail reload typically fixes it immediately, so the UI boundary
 * can auto-retry this specific signature without masking unrelated crashes.
 */
export function isRecoverableThreadBoundaryError(error: Error): boolean {
  return error.message.startsWith(ASSISTANT_UI_LOOKUP_ERROR_PREFIX);
}
