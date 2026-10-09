export type UsageInterval = { start: number; end: number };
export type UsageDay = { date: string; activeSeconds: number };
export const USAGE_HEARTBEAT_MS = 60_000;
export const USAGE_IDLE_MS = 5 * 60_000;

// Keep a short retry buffer. The server unions intervals from every tab/device,
// so duplicate delivery needs no separate client receipt storage.
export class UIUsageTracker {
  private last: number;
  private interaction: number;
  private foreground: boolean;
  private intervals: UsageInterval[] = [];

  constructor(now: number, foreground: boolean) {
    this.last = now;
    this.interaction = now;
    this.foreground = foreground;
  }

  checkpoint(now: number, foreground: boolean, interacted = false): void {
    const elapsed = now - this.last;
    // ponytail: gaps over 75s are suspend/throttling, not usage; resume on interaction.
    if (elapsed < 0 || elapsed > 75_000) {
      this.interaction = -Infinity;
    } else if (this.foreground) {
      const end = Math.min(now, this.interaction + USAGE_IDLE_MS);
      if (end > this.last) {
        const previous = this.intervals.at(-1);
        if (previous?.end === this.last) previous.end = end;
        else this.intervals.push({ start: this.last, end });
      }
    }
    this.last = now;
    this.foreground = foreground;
    if (interacted && foreground) this.interaction = now;
    const cutoff = now - 110_000;
    this.intervals = this.intervals.filter((interval) => interval.end > cutoff)
      .slice(-64).map((interval) => ({ start: Math.max(interval.start, cutoff), end: interval.end }));
  }

  snapshot(): UsageInterval[] {
    return this.intervals.map((interval) => ({ ...interval }));
  }

  acknowledge(until: number): void {
    this.intervals = this.intervals.filter((interval) => interval.end > until)
      .map((interval) => ({ start: Math.max(interval.start, until), end: interval.end }));
  }
}
