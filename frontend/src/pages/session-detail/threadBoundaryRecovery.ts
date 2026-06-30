/**
 * assistant-ui's client-lookup throw, matched by content rather than an
 * exact prefix. The library renamed the function `tapClientLookup` →
 * `useClientLookup` (store 0.1.x → 0.2.x) but the message body stayed
 * `<name>: Index N out of bounds (length: M)` / `Key "..." not found`.
 * Matching the stable tail (and the `ClientLookup:` marker) survives that
 * rename and any future one, while still scoping recovery to this specific
 * assistant-ui hiccup instead of masking unrelated crashes.
 */
const ASSISTANT_UI_LOOKUP_ERROR =
  /ClientLookup: (?:Index -?\d+ out of bounds|Key ".*" not found)/;

/**
 * assistant-ui occasionally throws a transient index/key lookup error while
 * its external-store view catches up with a freshly refreshed message tree.
 * A session-detail reload typically fixes it immediately, so the UI boundary
 * can auto-retry this specific signature without masking unrelated crashes.
 *
 * The crash is inherent to high-frequency external-store replacement on
 * React 19 (ocman's SSE streaming pattern): a component holding a stale
 * part index calls `get({index})` after the converted array shrank, so
 * `index === length`. Upstream fixes (assistant-ui#4077/#4069) reduce how
 * often it fires but don't eliminate the throw, so this reload remains the
 * safety net. See assistant-ui#4051 / #4573.
 */
export function isRecoverableThreadBoundaryError(error: Error): boolean {
  return ASSISTANT_UI_LOOKUP_ERROR.test(error.message);
}
