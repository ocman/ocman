import { describe, expect, it } from 'vitest';
import { UIUsageTracker, USAGE_IDLE_MS } from './uiUsage';

describe('UIUsageTracker', () => {
  it('counts foreground time, stops at idle cutoff, and resumes on interaction', () => {
    const tracker = new UIUsageTracker(0, true);
    for (let now = 60_000; now <= USAGE_IDLE_MS + 60_000; now += 60_000) {
      tracker.checkpoint(now, true);
      if (now <= USAGE_IDLE_MS) tracker.acknowledge(now);
    }
    expect(tracker.snapshot()).toEqual([]);
    tracker.checkpoint(420_000, true, true);
    tracker.checkpoint(480_000, true);
    expect(tracker.snapshot()).toEqual([{ start: 420_000, end: 480_000 }]);
  });

  it('accounts for focus transitions and coalesces interactions', () => {
    const tracker = new UIUsageTracker(0, true);
    tracker.checkpoint(10_000, false);
    tracker.checkpoint(20_000, true, true);
    tracker.checkpoint(25_000, true, true);
    tracker.checkpoint(30_000, true);
    expect(tracker.snapshot()).toEqual([{ start: 0, end: 10_000 }, { start: 20_000, end: 30_000 }]);
    tracker.acknowledge(25_000);
    expect(tracker.snapshot()).toEqual([{ start: 25_000, end: 30_000 }]);
  });

  it('never credits sleep or backwards clocks and bounds offline retries', () => {
    const tracker = new UIUsageTracker(0, true);
    tracker.checkpoint(60_000, true);
    tracker.checkpoint(600_000, true);
    expect(tracker.snapshot()).toEqual([]);
    tracker.checkpoint(660_000, true);
    expect(tracker.snapshot()).toEqual([]);
    tracker.checkpoint(650_000, true, true);
    tracker.checkpoint(700_000, true);
    expect(tracker.snapshot()).toEqual([{ start: 650_000, end: 700_000 }]);
    tracker.checkpoint(760_000, true);
    expect(tracker.snapshot()).toEqual([{ start: 650_000, end: 760_000 }]);
    tracker.checkpoint(820_000, true);
    expect(tracker.snapshot()).toEqual([{ start: 710_000, end: 820_000 }]);
  });
});
