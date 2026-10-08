/**
 * Coalesces event-driven refreshes: a burst of `schedule()` calls within
 * `delayMs` runs `run` once, at most one run is in flight, and a request
 * made during a run queues exactly one follow-up so the result is never
 * older than the last event. Nothing is ever aborted, so bursts of server
 * events no longer end as client-cancelled (499) requests.
 */
export function coalescedRefresh(run: () => Promise<unknown>, delayMs: number) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let inFlight = false;
  let again = false;

  const now = () => {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    if (inFlight) {
      again = true;
      return;
    }
    inFlight = true;
    void run().catch(() => {}).finally(() => {
      inFlight = false;
      if (again) {
        again = false;
        schedule();
      }
    });
  };
  const schedule = () => {
    if (timer === undefined && !inFlight) timer = setTimeout(now, delayMs);
    else if (inFlight) again = true;
  };
  const reset = () => {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    inFlight = false;
    again = false;
  };
  return { schedule, now, reset };
}

/**
 * Runs `run` with a signal aborted after `ms` or when `parent` aborts, so a
 * stalled request cannot hold a coalesced refresh in flight forever.
 */
export async function withDeadline<T>(ms: number, run: (signal: AbortSignal) => Promise<T>, parent?: AbortSignal): Promise<T> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  if (parent?.aborted) abort();
  parent?.addEventListener('abort', abort, { once: true });
  const timer = setTimeout(abort, ms);
  try {
    return await run(controller.signal);
  } finally {
    clearTimeout(timer);
    parent?.removeEventListener('abort', abort);
  }
}
