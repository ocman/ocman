import { describe, expect, it } from 'vitest';
import { withDeadline } from './coalescedRefresh';

describe('withDeadline', () => {
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
