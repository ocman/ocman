// DebouncedTrigger coalesces a stream of "something changed, please
// refetch" signals into actual fetches. Two timers cooperate:
//
//   - inner ("debounce"): each bump() resets a short timer (default
//     500 ms). When the inner timer fires the callback runs.
//   - outer ("maxWait"): started on the *first* bump of a burst and
//     never reset. When the outer timer fires the callback runs even
//     if the inner timer keeps getting kicked. This guarantees a
//     refetch within `maxWait` of the first bump regardless of how
//     busy the bump source is.
//
// Once either timer fires, both are cleared and the next bump starts a
// fresh burst. flushNow() runs the callback immediately and resets,
// which is what the manual "Refresh" buttons call.
//
// A tiny class instead of a React hook because we want the tests to
// drive bumps with vi.useFakeTimers() without renderHook plumbing.
// Hooks compose this object via a useRef.

export interface DebouncedTriggerOptions {
  /**
   * Resettable inner timer. Each bump() within an active burst pushes
   * the next callback firing this many milliseconds into the future.
   * Default 500.
   */
  debounceMs?: number;
  /**
   * Hard upper bound on how long a burst can keep the callback at
   * bay. Started on the first bump of a burst; not reset by
   * subsequent bumps. Default 5000.
   */
  maxWaitMs?: number;
}

export class DebouncedTrigger {
  private readonly callback: () => void;
  private readonly debounceMs: number;
  private readonly maxWaitMs: number;
  private innerTimer: ReturnType<typeof setTimeout> | null = null;
  private maxTimer: ReturnType<typeof setTimeout> | null = null;
  private cancelled = false;

  constructor(callback: () => void, opts: DebouncedTriggerOptions = {}) {
    this.callback = callback;
    this.debounceMs = opts.debounceMs ?? 500;
    this.maxWaitMs = opts.maxWaitMs ?? 5000;
  }

  // bump signals "another change happened". Schedules / re-schedules
  // the callback per the rules above. Safe to call repeatedly.
  bump(): void {
    if (this.cancelled) return;
    this.clearInner();
    this.innerTimer = setTimeout(() => this.fire(), this.debounceMs);
    if (this.maxTimer === null) {
      // First bump of a fresh burst — start the unconditional ceiling.
      this.maxTimer = setTimeout(() => this.fire(), this.maxWaitMs);
    }
  }

  // flushNow runs the callback immediately and clears any pending
  // timers. Used by the manual Refresh button so the user doesn't
  // have to wait out a debounce window after they click.
  flushNow(): void {
    if (this.cancelled) return;
    this.clearAll();
    this.callback();
  }

  // cancel stops any pending fire and prevents future bumps from
  // scheduling new ones. Used on hook unmount so a late timer doesn't
  // call into a torn-down component.
  cancel(): void {
    this.cancelled = true;
    this.clearAll();
  }

  // reset re-enables the trigger after cancel(). Used by the React
  // wrapper hook when the underlying request key (e.g. session id)
  // changes — we want to drop the previous burst and start fresh.
  reset(): void {
    this.clearAll();
    this.cancelled = false;
  }

  private fire(): void {
    this.clearAll();
    if (this.cancelled) return;
    this.callback();
  }

  private clearInner(): void {
    if (this.innerTimer !== null) {
      clearTimeout(this.innerTimer);
      this.innerTimer = null;
    }
  }

  private clearAll(): void {
    this.clearInner();
    if (this.maxTimer !== null) {
      clearTimeout(this.maxTimer);
      this.maxTimer = null;
    }
  }
}
