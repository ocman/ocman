import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { hello, oceEnvelope, textDelta } from '../../test/oce';
import { conversationStreamUrl, createConversationStream, SETTLE_RECONCILE_MS, STALE_RECONCILE_MS } from './conversationStream';
import type { OceEnvelope, OceEvent } from './events';

function setup(opts: { hasStale?: () => boolean; debug?: boolean; queue?: boolean } = {}) {
  const batches: OceEnvelope[][] = [];
  const calls = { reconcile: 0, dirty: 0 };
  const queued: unknown[] = [];
  const debug: unknown[] = [];
  const upstream = vi.fn();
  const stream = createConversationStream({
    sessionId: 's',
    dispatch: (action) => batches.push(action.envelopes),
    reconcile: async () => { calls.reconcile++; return true; },
    recover: async () => { calls.reconcile++; return true; },
    dirty: () => { calls.dirty++; },
    upstream,
    queue: opts.queue === false ? undefined : (messages) => queued.push(messages),
    hasStale: opts.hasStale,
    debug: opts.debug ? (rows) => debug.push(...rows) : undefined,
  });
  let listener: (e: MessageEvent) => void = () => {};
  const source = { addEventListener: (_: string, l: (e: MessageEvent) => void) => { listener = l; } } as unknown as EventSource;
  const send = (event: OceEvent, opts2: { epoch?: string; seq?: number; sessionId?: string } = {}) =>
    listener({ data: JSON.stringify(oceEnvelope(opts2.sessionId ?? 's', event, opts2)) } as MessageEvent);
  const raw = (data: unknown) => listener({ data } as MessageEvent);
  return { stream, source, send, raw, batches, calls, queued, debug, upstream };
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal('requestAnimationFrame', () => 1);
  vi.stubGlobal('cancelAnimationFrame', () => {});
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('conversationStreamUrl', () => {
  it('pins the owner and resumes from a cursor', () => {
    expect(conversationStreamUrl('a b')).toBe('/api/session/a%20b/stream');
    expect(conversationStreamUrl('s', 'r-x:opencode', 'e:1:5')).toBe('/api/session/s/stream?platform=r-x%3Aopencode&after=e%3A1%3A5');
  });
});

describe('createConversationStream', () => {
  it('reports upstream failure even while the HTTP stream stays open, and clears it on hello', () => {
    const { stream, source, send, upstream } = setup();
    stream.attach(source);
    send({ type: 'stream.error', code: 'owner-offline', message: 'owner disconnected', retryable: true });
    expect(upstream).toHaveBeenLastCalledWith('owner disconnected');
    send(hello(false));
    expect(upstream).toHaveBeenLastCalledWith(null);
  });

  it('refreshes the tree after a batch containing new children', () => {
    const { stream, source, send, calls } = setup();
    stream.attach(source);
    send({ type: 'child.upserted', child: { id: 'new', parentId: 's', title: 'Child', timeCreated: 1, timeUpdated: 1 } });
    stream.flush();
    expect(calls.reconcile).toBe(1);
  });
  it('ignores malformed, foreign and wrong-version frames', () => {
    const { stream, source, send, raw, batches } = setup();
    stream.attach(source);
    for (const data of ['', '  ', '{bad', 'null', '42', JSON.stringify({ v: 2, epoch: 'e', event: { type: 'x' } }), JSON.stringify({ v: 1, epoch: 'e' }), { not: 'a string' }]) raw(data);
    send({ type: 'session.status', status: 'busy' }, { sessionId: 'other' });
    stream.flush();
    expect(batches).toEqual([]);
    expect(stream.cursor()).toBeUndefined();
  });

  it('tracks the cursor, including an empty log hello without a seq', () => {
    const { stream, source, raw, send } = setup();
    stream.attach(source);
    const bare = { ...oceEnvelope('s', hello()), epoch: 'e1' } as Partial<OceEnvelope>;
    delete bare.seq;
    raw(JSON.stringify(bare));
    expect(stream.cursor()).toBe('e1:0');
    raw(JSON.stringify(bare));
    expect(stream.cursor()).toBe('e1:0');
    send({ type: 'turn.started', startedAt: 1 }, { epoch: 'e1', seq: 9 });
    expect(stream.cursor()).toBe('e1:9');
    const { seq: _seq, ...noSeq } = oceEnvelope('s', { type: 'turn.started', startedAt: 1 }, { epoch: 'e1' });
    void _seq;
    raw(JSON.stringify(noSeq));
    expect(stream.cursor()).toBe('e1:9');
  });

  it('reconciles a reconnect only when the owner could not resume it', () => {
    const { stream, source, send, calls, queued } = setup();
    stream.attach(source);
    send(hello(false, { status: { type: 'session.status', status: 'busy' }, prompts: [], judge: [],
      background: { type: 'background.changed', count: 0, tasks: [] }, inflight: { messages: [], parts: [] },
      liveConnection: true, queue: { type: 'queue.changed', messages: [] } }), { epoch: 'e1', seq: 1 });
    expect(calls).toEqual({ reconcile: 1, dirty: 1 });
    expect(queued).toEqual([[]]);
    stream.attach(source);
    send(hello(true), { epoch: 'e1', seq: 2 });
    expect(calls).toEqual({ reconcile: 1, dirty: 1 });
    stream.attach(source);
    send(hello(true), { epoch: 'e2', seq: 1 });
    expect(calls).toEqual({ reconcile: 2, dirty: 2 });
    stream.attach(source);
    send(hello(false), { epoch: 'e2', seq: 2 });
    expect(calls).toEqual({ reconcile: 3, dirty: 3 });
  });

  it('routes resets, rewrites, turn ends and queue changes', () => {
    const { stream, source, send, calls, queued } = setup({ queue: true });
    stream.attach(source);
    send({ type: 'stream.reset', reason: 'slow-consumer' });
    expect(calls.reconcile).toBe(0);
    send({ type: 'stream.reset', reason: 'evicted' });
    send({ type: 'history.rewritten', reason: 'compaction', fromMessageId: 'm' });
    send({ type: 'turn.ended', outcome: 'done', endedAt: 1 });
    expect(calls.reconcile).toBe(3);
    send({ type: 'queue.changed', messages: [{ id: 'q' }] as never });
    expect(queued).toEqual([[{ id: 'q' }]]);
    send({ type: 'session.status', status: 'done' });
    vi.advanceTimersByTime(SETTLE_RECONCILE_MS);
    expect(calls.reconcile).toBe(3);
  });

  it('restarts the settle fallback on a repeated idle edge and works without a queue listener', () => {
    const { stream, source, send, calls } = setup({ queue: false });
    stream.attach(source);
    send({ type: 'queue.changed', messages: [] });
    const idle: OceEvent = { type: 'session.status', status: 'done', provisional: true };
    send(idle);
    vi.advanceTimersByTime(SETTLE_RECONCILE_MS - 1);
    send(idle);
    vi.advanceTimersByTime(SETTLE_RECONCILE_MS - 1);
    expect(calls.reconcile).toBe(0);
    vi.advanceTimersByTime(1);
    expect(calls.reconcile).toBe(1);
  });

  it('batches content until the frame and reconciles parts that stay stale', () => {
    let stale = true;
    const { stream, source, send, batches, calls } = setup({ hasStale: () => stale });
    stream.attach(source);
    send(textDelta('p', 0, 'a'));
    send(textDelta('p', 1, 'b'));
    expect(batches).toEqual([]);
    vi.advanceTimersByTime(50);
    expect(batches).toHaveLength(1);
    expect(batches[0]).toHaveLength(2);
    send(textDelta('p', 9, 'gap'));
    stream.flush();
    vi.advanceTimersByTime(STALE_RECONCILE_MS);
    expect(calls.reconcile).toBe(1);
    stale = false;
    send(textDelta('p', 2, 'c'));
    stream.flush();
    vi.advanceTimersByTime(STALE_RECONCILE_MS);
    expect(calls.reconcile).toBe(1);
  });

  it('records a debug ring and returns undelivered envelopes on dispose', () => {
    const { stream, source, send, raw, debug, batches, calls } = setup({ debug: true, hasStale: () => true });
    stream.attach(source);
    raw('{bad');
    send(textDelta('p', 0, 'a'));
    stream.flush();
    expect(debug).toHaveLength(2);
    send(textDelta('p', 1, 'b'));
    const pending = stream.dispose();
    expect(pending.map((e) => e.event.type)).toEqual(['part.delta']);
    send(textDelta('p', 2, 'c'));
    vi.advanceTimersByTime(STALE_RECONCILE_MS);
    expect(batches).toHaveLength(1);
    expect(calls.reconcile).toBe(0);
  });
});
