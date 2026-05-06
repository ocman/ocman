/**
 * Exponential-backoff schedule for SSE reconnects.
 *
 * The first retry fires fast (500 ms, no jitter) so a transient
 * network blip is invisible. Subsequent retries double the target
 * delay and apply *equal jitter* — half deterministic, half random —
 * so the "next retry in Xs" countdown displayed in the UI stays
 * roughly accurate while still spreading out reconnect storms across
 * clients.
 *
 * The schedule is capped at one minute. With these constants the
 * sequence is approximately:
 *
 *   attempt 0 → 500 ms (exact)
 *   attempt 1 → 500–1000 ms
 *   attempt 2 → 1–2 s
 *   attempt 3 → 2–4 s
 *   attempt 4 → 4–8 s
 *   attempt 5 → 8–16 s
 *   attempt 6 → 16–32 s
 *   attempt 7+ → 30–60 s (capped)
 */

/** Floor for the very first reconnect after a disconnect. */
export const SSE_BACKOFF_BASE_MS = 500;

/** Hard ceiling: never wait longer than this between attempts. */
export const SSE_BACKOFF_MAX_MS = 60_000;

/**
 * Compute the delay (ms) before the n-th reconnect attempt.
 *
 * @param attempt zero-based reconnect attempt counter. `0` is the
 *                first retry after a disconnect.
 * @param random  injectable RNG for tests; defaults to `Math.random`.
 */
export function computeReconnectDelay(
  attempt: number,
  random: () => number = Math.random,
): number {
  // Defensive: a stale/negative counter shouldn't collapse into a
  // tight reconnect loop.
  const safeAttempt = Math.max(0, Math.floor(attempt));
  if (safeAttempt === 0) return SSE_BACKOFF_BASE_MS;

  // 2^attempt grows fast; clamp before applying jitter so the jitter
  // window is anchored to the cap, not to the unbounded exponent.
  const target = Math.min(
    SSE_BACKOFF_MAX_MS,
    SSE_BACKOFF_BASE_MS * 2 ** safeAttempt,
  );

  // Equal jitter: half the window is deterministic, half is random.
  // This keeps the displayed countdown meaningful while still
  // de-synchronising clients.
  const half = target / 2;
  const jittered = half + random() * half;
  return Math.min(SSE_BACKOFF_MAX_MS, Math.round(jittered));
}
