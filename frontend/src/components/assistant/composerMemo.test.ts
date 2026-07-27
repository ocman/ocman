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

  it('re-renders when session tree usage changes', () => {
    expect(composerPropsEqual(
      { ...base, sessionTreeStats: { input: 10, output: 20, totalCost: 0.1, sessions: 2 } },
      { ...base, sessionTreeStats: { input: 30, output: 20, totalCost: 0.1, sessions: 2 } },
    )).toBe(false);
  });

  // Regression: Composer copies shellExec into a ref inside an effect
  // keyed on [shellExec] and only ever reads the ref. If the comparator
  // omits shellExec, a false->true flip (capabilities arriving after
  // first paint) skips the render, so the effect never runs and the ref
  // stays false — a `!ls` command is sent to the LLM as a plain prompt.
  it('re-renders when shellExec capability flips', () => {
    expect(composerPropsEqual(
      { ...base, shellExec: false },
      { ...base, shellExec: true },
    )).toBe(false);
    expect(composerPropsEqual(
      { ...base, shellExec: undefined },
      { ...base, shellExec: true },
    )).toBe(false);
    expect(composerPropsEqual(
      { ...base, shellExec: true },
      { ...base, shellExec: true },
    )).toBe(true);
  });
});
