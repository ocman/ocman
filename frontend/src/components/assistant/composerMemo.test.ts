import { describe, it, expect } from 'vitest';
import { composerPropsEqual } from './composerMemo';

// Regression: the follow-up queue is driven by the queuedMessages prop.
// If the memo comparator ignores it, switching sessions (or a queue
// change) leaves the composer showing a stale/empty queue until a full
// page refresh remounts it. The comparator must NOT treat a changed
// queuedMessages as "equal".
describe('composerPropsEqual — queuedMessages', () => {
  const base = { isRunning: false, queuedMessages: [] } as Parameters<typeof composerPropsEqual>[0];

  it('re-renders when queuedMessages identity changes', () => {
    const prev = { ...base, queuedMessages: [] };
    const next = { ...base, queuedMessages: [{ id: '1', text: 'hi', hasImages: false }] };
    expect(composerPropsEqual(prev, next)).toBe(false);
  });

  it('skips re-render when nothing relevant changed', () => {
    const q = [{ id: '1', text: 'hi', hasImages: false }];
    expect(composerPropsEqual({ ...base, queuedMessages: q }, { ...base, queuedMessages: q })).toBe(true);
  });

  it('re-renders when active duration changes', () => {
    expect(composerPropsEqual(
      { ...base, activeDurationMs: 1_000 },
      { ...base, activeDurationMs: 2_000 },
    )).toBe(false);
  });
});
