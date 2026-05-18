/**
 * Truncation helpers shared between the SSE handler (in
 * `useSession`) and the wire-shape extractor (`extractMessageFromEvent`
 * in `sseHelpers.ts`).
 *
 * The historical `mergeParts` / `upsertPart` / `reparentTempParts` /
 * `insertMessageByTime` / `inferStatusFromMessage` helpers are gone:
 * the new pipeline (`sessionReducer.ts` + `useSession.ts`) owns those
 * state transitions itself. See spec/sse-rewrite/architecture.md.
 */

/**
 * Maximum length for part text/output strings before they are truncated
 * in the frontend cache. Mirrors the backend's `maxOutputLen`.
 */
export const MAX_OUTPUT_LEN = 200_000;

/**
 * Truncate large string fields on a Part to keep memory usage bounded.
 * Non-string values are returned unchanged. The truncation marker is the
 * same one the backend uses, so the UI doesn't need to special-case
 * frontend- vs backend-truncated payloads.
 */
export function truncatePartField(value: unknown): unknown {
  if (typeof value === 'string' && value.length > MAX_OUTPUT_LEN) {
    return value.slice(0, MAX_OUTPUT_LEN) + '\n... (truncated)';
  }
  return value;
}
