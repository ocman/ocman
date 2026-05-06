// @vitest-environment jsdom
//
// Tests for the rAF-coalesced delta buffer used by useSessionSSE.
// These tests pin the contract:
//
//   - many enqueue() calls within a frame produce ONE setParts call;
//   - the resulting parts state has all deltas applied in order;
//   - deltas to nested fields (`text.value`) work like flat ones;
//   - flush() commits synchronously and is a no-op when empty;
//   - cancel() drops the buffer and any pending rAF;
//   - deltas that arrive AFTER a flush schedule a fresh frame.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Part } from '../../lib/api';
import { applyDelta, createSseDeltaBuffer } from './sseDeltaBuffer';

beforeEach(() => {
  // Use fake timers so we control when the setTimeout(16) fallback
  // (jsdom doesn't ship rAF) fires. Tests advance the clock to flush.
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

function makeStateMachine(initial: Part[] = []) {
  let state = initial;
  const setParts = vi.fn((updater: Part[] | ((prev: Part[]) => Part[])) => {
    state = typeof updater === 'function' ? (updater as (prev: Part[]) => Part[])(state) : updater;
  });
  return { setParts, get: () => state };
}

function getPartData(parts: Part[], id: string): Record<string, unknown> {
  const p = parts.find((q) => q.id === id);
  if (!p) throw new Error(`part ${id} not found`);
  return typeof p.data === 'string'
    ? JSON.parse(p.data) as Record<string, unknown>
    : p.data as unknown as Record<string, unknown>;
}

describe('createSseDeltaBuffer', () => {
  it('coalesces many enqueues within one frame into a single setParts call', () => {
    const sm = makeStateMachine();
    const buffer = createSseDeltaBuffer(sm.setParts);

    for (let i = 0; i < 50; i++) {
      buffer.enqueue({
        partId: 'p1',
        messageId: 'm1',
        sessionId: 's1',
        field: 'text',
        delta: 'x',
      });
    }
    expect(sm.setParts).not.toHaveBeenCalled();

    vi.advanceTimersByTime(20);

    expect(sm.setParts).toHaveBeenCalledTimes(1);
    expect((getPartData(sm.get(), 'p1').text as string)).toBe('x'.repeat(50));
  });

  it('appends to existing parts, preserving previously-loaded data', () => {
    const initial: Part[] = [
      {
        id: 'p1',
        messageId: 'm1',
        sessionId: 's1',
        data: { type: 'text', text: 'hello' } as unknown as string,
      },
    ];
    const sm = makeStateMachine(initial);
    const buffer = createSseDeltaBuffer(sm.setParts);

    buffer.enqueue({ partId: 'p1', messageId: 'm1', sessionId: 's1', field: 'text', delta: ' world' });
    vi.advanceTimersByTime(20);

    expect((getPartData(sm.get(), 'p1').text as string)).toBe('hello world');
  });

  it('creates a new part when the delta arrives before message.part.updated', () => {
    const sm = makeStateMachine();
    const buffer = createSseDeltaBuffer(sm.setParts);

    buffer.enqueue({ partId: 'fresh', messageId: 'm1', sessionId: 's1', field: 'text', delta: 'first' });
    vi.advanceTimersByTime(20);

    expect(sm.get()).toHaveLength(1);
    expect(sm.get()[0].id).toBe('fresh');
    expect((getPartData(sm.get(), 'fresh').text as string)).toBe('first');
  });

  it('writes nested fields via dotted notation', () => {
    const sm = makeStateMachine();
    const buffer = createSseDeltaBuffer(sm.setParts);

    buffer.enqueue({ partId: 'p1', messageId: 'm1', sessionId: 's1', field: 'tool.input', delta: 'foo' });
    buffer.enqueue({ partId: 'p1', messageId: 'm1', sessionId: 's1', field: 'tool.input', delta: 'bar' });
    vi.advanceTimersByTime(20);

    const part = getPartData(sm.get(), 'p1');
    expect((part.tool as Record<string, unknown>).input).toBe('foobar');
  });

  it('applies multiple distinct fields on the same part in a single commit', () => {
    const sm = makeStateMachine();
    const buffer = createSseDeltaBuffer(sm.setParts);

    buffer.enqueue({ partId: 'p1', messageId: 'm1', sessionId: 's1', field: 'text', delta: 'aa' });
    buffer.enqueue({ partId: 'p1', messageId: 'm1', sessionId: 's1', field: 'reasoning', delta: 'rr' });
    vi.advanceTimersByTime(20);

    expect(sm.setParts).toHaveBeenCalledTimes(1);
    const data = getPartData(sm.get(), 'p1');
    expect(data.text).toBe('aa');
    expect(data.reasoning).toBe('rr');
  });

  it('flushes pending deltas synchronously when flush() is called', () => {
    const sm = makeStateMachine();
    const buffer = createSseDeltaBuffer(sm.setParts);

    buffer.enqueue({ partId: 'p1', messageId: 'm1', sessionId: 's1', field: 'text', delta: 'now' });
    expect(sm.setParts).not.toHaveBeenCalled();

    buffer.flush();
    expect(sm.setParts).toHaveBeenCalledTimes(1);
    expect((getPartData(sm.get(), 'p1').text as string)).toBe('now');

    // Pending rAF was cancelled, so advancing timers is a no-op.
    vi.advanceTimersByTime(100);
    expect(sm.setParts).toHaveBeenCalledTimes(1);
  });

  it('flush() is a no-op when the buffer is empty', () => {
    const sm = makeStateMachine();
    const buffer = createSseDeltaBuffer(sm.setParts);
    buffer.flush();
    expect(sm.setParts).not.toHaveBeenCalled();
  });

  it('cancel() drops pending deltas without writing them', () => {
    const sm = makeStateMachine();
    const buffer = createSseDeltaBuffer(sm.setParts);

    buffer.enqueue({ partId: 'p1', messageId: 'm1', sessionId: 's1', field: 'text', delta: 'lost' });
    buffer.cancel();
    vi.advanceTimersByTime(100);

    expect(sm.setParts).not.toHaveBeenCalled();
  });

  it('schedules a fresh frame for deltas that arrive after a flush', () => {
    const sm = makeStateMachine();
    const buffer = createSseDeltaBuffer(sm.setParts);

    buffer.enqueue({ partId: 'p1', messageId: 'm1', sessionId: 's1', field: 'text', delta: 'a' });
    vi.advanceTimersByTime(20);
    expect(sm.setParts).toHaveBeenCalledTimes(1);

    buffer.enqueue({ partId: 'p1', messageId: 'm1', sessionId: 's1', field: 'text', delta: 'b' });
    expect(sm.setParts).toHaveBeenCalledTimes(1); // not flushed yet
    vi.advanceTimersByTime(20);
    expect(sm.setParts).toHaveBeenCalledTimes(2);
    expect((getPartData(sm.get(), 'p1').text as string)).toBe('ab');
  });

  it('coalesces deltas across many distinct parts in one frame', () => {
    const sm = makeStateMachine();
    const buffer = createSseDeltaBuffer(sm.setParts);

    for (let i = 0; i < 10; i++) {
      buffer.enqueue({
        partId: `p${i}`,
        messageId: `m${i}`,
        sessionId: 's1',
        field: 'text',
        delta: `chunk${i}`,
      });
    }
    vi.advanceTimersByTime(20);

    expect(sm.setParts).toHaveBeenCalledTimes(1);
    expect(sm.get()).toHaveLength(10);
    for (let i = 0; i < 10; i++) {
      expect((getPartData(sm.get(), `p${i}`).text as string)).toBe(`chunk${i}`);
    }
  });
});

describe('applyDelta (pure helper)', () => {
  it('handles malformed string-encoded data gracefully', () => {
    const prev: Part[] = [
      { id: 'p1', messageId: 'm1', sessionId: 's1', data: '<<<not json>>>' as unknown as string },
    ];
    const result = applyDelta(prev, {
      partId: 'p1',
      messageId: 'm1',
      sessionId: 's1',
      fieldDeltas: new Map([['text', 'recovered']]),
    });
    const data = typeof result[0].data === 'string'
      ? JSON.parse(result[0].data) as Record<string, unknown>
      : result[0].data as unknown as Record<string, unknown>;
    expect(data.text).toBe('recovered');
  });

  it('does not mutate the input array', () => {
    const prev: Part[] = [
      { id: 'p1', messageId: 'm1', sessionId: 's1', data: { type: 'text', text: 'a' } as unknown as string },
    ];
    const result = applyDelta(prev, {
      partId: 'p1',
      messageId: 'm1',
      sessionId: 's1',
      fieldDeltas: new Map([['text', 'b']]),
    });
    expect(result).not.toBe(prev);
    expect((prev[0].data as unknown as Record<string, unknown>).text).toBe('a');
  });
});
