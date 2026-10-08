import { describe, expect, it, vi } from 'vitest';
import { withDeadline } from './coalescedRefresh';

describe('withDeadline', () => {
  it('rejects a stalled operation even when it ignores cancellation', async () => {
    vi.useFakeTimers();
    try {
      let signal!: AbortSignal;
      const pending = withDeadline(15_000, (s) => { signal = s; return new Promise(() => {}); });
      const rejected = expect(pending).rejects.toMatchObject({ name: 'TimeoutError' });
      await vi.advanceTimersByTimeAsync(15_000);
      await rejected;
      expect(signal.aborted).toBe(true);
    } finally { vi.useRealTimers(); }
  });
  it('forwards a parent abort, including one that already happened', async () => {
    const parent = new AbortController();
    const seen: AbortSignal[] = [];
    const pending = withDeadline(60_000, (signal) => { seen.push(signal); return Promise.resolve(1); }, parent.signal);
    parent.abort();
    await pending;
    expect(seen[0].aborted).toBe(true);
    await withDeadline(60_000, (signal) => { seen.push(signal); return Promise.resolve(1); }, parent.signal);
    expect(seen[1].aborted).toBe(true);
  });
});
