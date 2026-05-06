import { describe, expect, it } from 'vitest';
import {
  SSE_BACKOFF_BASE_MS,
  SSE_BACKOFF_MAX_MS,
  computeReconnectDelay,
} from './sseBackoff';

describe('computeReconnectDelay', () => {
  // Deterministic random for testing the jittered branch.
  const stableRandom = () => 0.5;

  it('returns the base delay for the first attempt (no jitter)', () => {
    // The very first reconnect happens fast and deterministically so a
    // transient blip recovers without visible churn.
    expect(computeReconnectDelay(0, stableRandom)).toBe(SSE_BACKOFF_BASE_MS);
  });

  it('doubles each subsequent attempt before jitter is applied', () => {
    // Equal jitter on attempt 1: target = 1000, delay = 500 + random*500.
    // With random=0.5, delay = 750.
    expect(computeReconnectDelay(1, stableRandom)).toBe(750);
    // Attempt 2: target = 2000, delay = 1000 + random*1000 = 1500.
    expect(computeReconnectDelay(2, stableRandom)).toBe(1500);
    // Attempt 3: target = 4000, delay = 2000 + random*2000 = 3000.
    expect(computeReconnectDelay(3, stableRandom)).toBe(3000);
  });

  it('caps the target delay at SSE_BACKOFF_MAX_MS', () => {
    // Attempt 20 would be astronomical without the cap; with the cap
    // it should fall in [max/2, max].
    const delay = computeReconnectDelay(20, () => 1);
    expect(delay).toBeLessThanOrEqual(SSE_BACKOFF_MAX_MS);
    expect(delay).toBeGreaterThanOrEqual(SSE_BACKOFF_MAX_MS / 2);
  });

  it('never returns a delay below the base for attempts >= 1', () => {
    // Equal jitter floor is target/2, which for attempt 1 is 500ms —
    // matching the base. random=0 should land exactly on the floor.
    expect(computeReconnectDelay(1, () => 0)).toBe(SSE_BACKOFF_BASE_MS);
  });

  it('never exceeds the max even when random=1', () => {
    for (let attempt = 0; attempt <= 30; attempt++) {
      const delay = computeReconnectDelay(attempt, () => 1);
      expect(delay).toBeLessThanOrEqual(SSE_BACKOFF_MAX_MS);
    }
  });

  it('treats negative attempts as the first attempt', () => {
    // Defensive: a stale/negative counter shouldn't underflow into a
    // zero-delay tight loop.
    expect(computeReconnectDelay(-1, stableRandom)).toBe(SSE_BACKOFF_BASE_MS);
  });
});
