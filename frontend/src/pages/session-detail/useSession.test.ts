// @vitest-environment jsdom
//
// Tests for `useSession` — the hook that wraps the pure
// `sessionReducer` with the EventSource lifecycle. The hook is
// intentionally small (~80 lines) so the test surface is
// proportional: lifecycle, dispatch routing, reconnect, refetch on
// idle. We do not re-test the reducer here — see
// `sessionReducer.test.ts` for the state-transition table.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import type { SessionDetail } from '../../lib/api';
import { aggregateSessionTreeStats } from '../../lib/sessionStatus';
import { useSession } from './useSession';
import { useApiStore } from '../../lib/apiStore';
import { KEEPALIVE_TIMEOUT_MS } from '../../lib/sseKeepalive';
import { SIDEBAR_SESSIONS_KEY, getSidebarSessions } from '../../lib/sessionsCache';
import { queryClient } from '../../lib/queryClient';
import type { OceEvent } from '../../lib/conversation/events';
import { PROGRESS_RECONCILE_MS, SETTLE_RECONCILE_MS, STALE_RECONCILE_MS } from '../../lib/conversation/conversationStream';
import { COMMIT_READ_DELAY_MS } from './useSessionLifecycle';
import { onQueueUpdated } from '../../lib/useGlobalEvents';
import { remoteLog } from '../../lib/remoteLog';
import { APIError, AuthError } from '../../lib/api.requests';
import { hello, oceEnvelope, partUpserted, streamSessionId, textDelta } from '../../test/oce';

// ---------- Fake EventSource ----------

class FakeEventSource {
  static OPEN = 1 as const;
  static CONNECTING = 0 as const;
  static CLOSED = 2 as const;
  static instances: FakeEventSource[] = [];

  url: string;
  readyState: number = FakeEventSource.CONNECTING;
  withCredentials = false;
  onopen: ((ev: Event) => unknown) | null = null;
  onmessage: ((ev: MessageEvent) => unknown) | null = null;
  onerror: ((ev: Event) => unknown) | null = null;
  listeners: Record<string, Array<(ev: MessageEvent) => unknown>> = {};
  closed = false;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(name: string, cb: (ev: MessageEvent) => unknown) {
    (this.listeners[name] ||= []).push(cb);
  }

  removeEventListener() {}

  open() {
    this.readyState = FakeEventSource.OPEN;
    this.onopen?.(new Event('open'));
  }

  emitMessage(data: unknown) {
    const payload = typeof data === 'string' ? data : JSON.stringify(data);
    const event = new MessageEvent('message', { data: payload });
    this.onmessage?.(event);
    for (const cb of this.listeners.message || []) cb(event);
  }

  emitOce(event: OceEvent, opts?: { sessionId?: string; epoch?: string; seq?: number }) {
    this.emitNamed('oce', oceEnvelope(opts?.sessionId ?? streamSessionId(this.url), event, opts));
  }

  emitNamed(name: string, data: unknown) {
    const payload = typeof data === 'string' ? data : JSON.stringify(data);
    const event = new MessageEvent(name, { data: payload });
    for (const cb of this.listeners[name] || []) cb(event);
  }

  error() {
    this.readyState = FakeEventSource.CLOSED;
    this.onerror?.(new Event('error'));
  }

  close() {
    this.closed = true;
    this.readyState = FakeEventSource.CLOSED;
  }

  static latest(): FakeEventSource | undefined {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1];
  }

  static reset() {
    FakeEventSource.instances.length = 0;
  }
}

(globalThis as unknown as { EventSource: typeof FakeEventSource }).EventSource = FakeEventSource;

// ---------- Fixtures ----------

const SID = 'sess-1';

it('routes a notification deep link to its explicit owner instead of a same-id cached owner', async () => {
  const detail = makeDetail();
  useApiStore.getState().setCachedSession(SID, detail);
  const fetchSession = vi.fn().mockResolvedValue({ ...detail, session: { ...detail.session, platform: 'r-laptop:opencode' } });
  const { unmount } = renderHook(() => useSession(SID, { platform: 'r-laptop:opencode', fetchSession }));
  await waitFor(() => expect(fetchSession).toHaveBeenCalledWith(SID, 30, 0, expect.any(AbortSignal), 'r-laptop:opencode', false));
  unmount();
});

function makeDetail(overrides: Partial<SessionDetail> = {}): SessionDetail {
  return {
    session: {
      id: SID,
      platform: 'opencode',
      projectId: 'proj-1',
      title: 'Test session',
      directory: '/tmp/proj',
      timeCreated: 1_000,
      timeUpdated: 2_000,
      summaryAdditions: null,
      summaryDeletions: null,
      summaryFiles: null,
      shareUrl: null,
      messageCount: 0,
      durationMs: 0,
      activeDurationMs: 0,
      totalInputTokens: 0,
      totalOutputTokens: 0,
      totalCost: 0,
      status: 'done',
      liveConnection: true,
      pendingPermission: false,
      pendingQuestion: false,
      archived: false,
      seen: true,
      pinned: false,
      pinnedAt: 0,
      seenTimeUpdated: 0,
      unreadCount: 0,
    },
    messages: [],
    parts: [],
    totalMessages: 0,
    contextTokenCount: 0,
    defaultAgent: 'build',
    defaultModel: 'claude-opus-4',
    ...overrides,
  };
}

// ---------- Test scaffolding ----------

beforeEach(() => {
  FakeEventSource.reset();
  queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, []);
  useApiStore.setState({sessionCache: new Map(), sessionCacheOrder: [] });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('useSession — bounded stream work (#722)', () => {
  it('removes cached history deleted while the page was unmounted', async () => {
    useApiStore.getState().setCachedSession(SID, makeDetail({
      messages: [{ id: 'removed', sessionId: SID, timeCreated: 1, data: { role: 'assistant' } }],
      parts: [{ id: 'removed-part', messageId: 'removed', sessionId: SID, data: JSON.stringify({ type: 'text', text: 'deleted while away' }) }],
    }));
    let finish!: (d: SessionDetail) => void;
    const fetchSession = vi.fn(() => new Promise<SessionDetail>((resolve) => { finish = resolve; }));
    const { result, unmount } = renderHook(() => useSession(SID, { fetchSession }));
    expect(result.current.messages.map((m) => m.id)).toContain('removed');
    await act(async () => finish(makeDetail()));
    expect(result.current.messages).toEqual([]);
    expect(result.current.parts).toEqual([]);
    unmount();
  });

  const delta = (offset: number, text: string) => textDelta('p', offset, text);
  const busy: OceEvent = { type: 'session.status', status: 'busy', notice: null };
  const idleEdge: OceEvent = { type: 'session.status', status: 'done', provisional: true, notice: null };
  const turnEnded = (outcome: 'done' | 'error' = 'done'): OceEvent => ({ type: 'turn.ended', outcome, endedAt: 1 });
  const textOf = (parts: SessionDetail['parts']) => {
    const data = parts.find((p) => p.id === 'p')?.data;
    return (typeof data === 'string' ? JSON.parse(data) : data)?.text;
  };
  async function setup(debug = false) {
    vi.useFakeTimers();
    let frame: FrameRequestCallback = () => {};
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => { frame = cb; return 1; });
    vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
    const fetchSession = vi.fn(async (id: string) => {
      const detail = makeDetail();
      return { ...detail, session: { ...detail.session, id } };
    });
    const hook = renderHook(({ id }) => useSession(id, { fetchSession, debug }), { initialProps: { id: SID } });
    await act(async () => {});
    return { ...hook, fetchSession, sse: FakeEventSource.latest()!, frame: () => act(() => frame(0)) };
  }

  it('recovers missing conversation output after a transient turn-end read failure without a page refresh', async () => {
    const { result, sse, fetchSession, unmount } = await setup();
    const completed = makeDetail({
      messages: [{ id: 'reply', sessionId: SID, timeCreated: 3, data: { role: 'assistant' } }],
      parts: [{ id: 'reply-part', messageId: 'reply', sessionId: SID, data: JSON.stringify({ type: 'text', text: 'Recovered response' }) }],
    });
    fetchSession.mockRejectedValueOnce(new Error('temporary read failure')).mockResolvedValue(completed);
    await act(async () => sse.emitOce(turnEnded()));
    expect(result.current.refreshError).toBe('temporary read failure');
    expect(result.current.messages).toEqual([]);
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(result.current.messages.map((m) => m.id)).toContain('reply');
    expect(result.current.refreshError).toBeNull();
    unmount();
  });

  it('retries failed cold-stream recovery and preserves events from the failed read', async () => {
    const { result, sse, fetchSession, unmount } = await setup();
    let reject!: (reason: Error) => void;
    fetchSession.mockReturnValueOnce(new Promise<SessionDetail>((_, fail) => { reject = fail; }))
      .mockRejectedValueOnce(new Error('temporary read failure'));
    act(() => {
      sse.emitOce(hello(false));
      sse.emitOce(partUpserted({ id: 'p', messageId: 'm', data: { type: 'text', text: 'Final streamed response' } }, SID, true));
      sse.emitOce(turnEnded());
    });
    // The turn-end read supersedes the cold read, but fails too.
    await act(async () => {
      reject(new Error('superseded'));
    });
    expect(result.current.refreshError).toBe('temporary read failure');
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(result.current.refreshError).toBeNull();
    expect(textOf(result.current.parts)).toBe('Final streamed response');
    unmount();
  });

  it('renders a sent prompt and response when content and terminal events are missed on an open stream', async () => {
    const { result, sse, fetchSession, unmount } = await setup();
    const messages = [
      { id: 'user', sessionId: SID, timeCreated: 1, data: { role: 'user' as const } },
      { id: 'assistant', sessionId: SID, timeCreated: 2, data: { role: 'assistant' as const } },
    ];
    const parts = messages.map((m) => ({ id: `${m.id}-part`, messageId: m.id, sessionId: SID,
      data: JSON.stringify({ type: 'text', text: m.id === 'user' ? 'My prompt' : 'My response' }) }));
    fetchSession.mockResolvedValue(makeDetail({ messages, parts }));
    act(() => sse.emitOce(busy));
    expect(result.current.messages).toEqual([]);
    await act(async () => vi.advanceTimersByTimeAsync(PROGRESS_RECONCILE_MS));
    expect(result.current.messages).toEqual(messages);
    expect(result.current.parts).toEqual(parts);
    expect(result.current.session?.status).toBe('done');
    expect(sse.closed).toBe(false);
    const reads = fetchSession.mock.calls.length;
    await act(async () => vi.advanceTimersByTimeAsync(PROGRESS_RECONCILE_MS * 2));
    expect(fetchSession).toHaveBeenCalledTimes(reads);
    unmount();
  });

  it('backs off failed reads, cancels retries after success, and drops them on navigation', async () => {
    const { result, fetchSession, rerender, unmount } = await setup();
    fetchSession.mockRejectedValue(new Error('offline'));
    await act(async () => result.current.refresh());
    const initialReads = fetchSession.mock.calls.length;
    for (const [index, delay] of [1_000, 2_000, 4_000, 8_000, 15_000, 15_000].entries()) {
      await act(async () => vi.advanceTimersByTimeAsync(delay - 1));
      expect(fetchSession).toHaveBeenCalledTimes(initialReads + index);
      await act(async () => vi.advanceTimersByTimeAsync(1));
      expect(fetchSession).toHaveBeenCalledTimes(initialReads + index + 1);
    }
    fetchSession.mockResolvedValue(makeDetail());
    await act(async () => result.current.refresh());
    const successfulReads = fetchSession.mock.calls.length;
    await act(async () => vi.advanceTimersByTimeAsync(15_000));
    expect(fetchSession).toHaveBeenCalledTimes(successfulReads);
    fetchSession.mockRejectedValue(new Error('offline'));
    await act(async () => result.current.refresh());
    await act(async () => rerender({ id: 'new' }));
    const readsAfterNavigation = fetchSession.mock.calls.length;
    await act(async () => vi.advanceTimersByTimeAsync(15_000));
    expect(fetchSession).toHaveBeenCalledTimes(readsAfterNavigation);
    unmount();
  });

  it('returns to live after an initial read failure recovers automatically', async () => {
    vi.useFakeTimers();
    const fetchSession = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue(makeDetail());
    const { result, unmount } = renderHook(() => useSession(SID, { fetchSession }));
    await act(async () => {});
    expect(result.current.status).toBe('error');
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(result.current.status).toBe('live');
    expect(result.current.loadError).toBeNull();
    expect(result.current.session?.id).toBe(SID);
    unmount();
  });

  it('retains cached streamed text when retrying a failed page restoration', async () => {
    vi.useFakeTimers();
    const message = { id: 'm', sessionId: SID, timeCreated: 1, data: { role: 'assistant' as const } };
    const part = { id: 'p', messageId: 'm', sessionId: SID, data: JSON.stringify({ type: 'text', text: 'Streamed ahead' }) };
    const detail = makeDetail({ session: { ...makeDetail().session, status: 'busy' }, messages: [message], parts: [part] });
    useApiStore.getState().setCachedSession(SID, detail);
    const fetchSession = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue({
      ...detail, parts: [{ ...part, data: JSON.stringify({ type: 'text', text: 'Streamed' }) }],
    });
    const { result, unmount } = renderHook(() => useSession(SID, { fetchSession }));
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(result.current.refreshError).toBeNull();
    expect(textOf(result.current.parts)).toBe('Streamed ahead');
    unmount();
  });

  it.each([new APIError('not found', 404), new APIError('forbidden', 403), new AuthError()])('does not automatically retry permanent read failures: %s', async (error) => {
    vi.useFakeTimers();
    const fetchSession = vi.fn().mockRejectedValue(error);
    const { unmount } = renderHook(() => useSession(SID, { fetchSession }));
    await act(async () => vi.advanceTimersByTimeAsync(15_000));
    expect(fetchSession).toHaveBeenCalledTimes(1);
    unmount();
  });

  it('keeps an explicit reload authoritative when its first read fails', async () => {
    const { result, fetchSession, unmount } = await setup();
    act(() => result.current.hydrateHistory(
      [{ id: 'removed', sessionId: SID, timeCreated: 1, data: { role: 'user' } }], [],
    ));
    fetchSession.mockRejectedValueOnce(new Error('offline'));
    await act(async () => result.current.reload());
    expect(result.current.messages.map((m) => m.id)).toContain('removed');
    await act(async () => vi.advanceTimersByTimeAsync(1_000));
    expect(result.current.messages).toEqual([]);
    unmount();
  });

  it('keeps the sidebar running after navigating away from a live session', async () => {
    const row = makeDetail().session;
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [row, { ...row, platform: 'r-box:opencode' }]);
    const { result, sse, frame, rerender, unmount } = await setup();
    act(() => sse.emitOce(busy));
    frame();
    expect(result.current.session?.status).toBe('busy');
    await act(async () => rerender({ id: 'other' }));
    expect(getSidebarSessions().find(s => s.id === SID)?.status).toBe('busy');
    expect(getSidebarSessions().find(s => s.platform === 'r-box:opencode')?.status).toBe('done');
    unmount();
  });

  it('records a repeated live busy status as a newer sidebar observation', async () => {
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [makeDetail().session]);
    const { sse, unmount } = await setup();
    act(() => sse.emitOce(busy));
    const revision = useApiStore.getState().recentStatusRevisions[`opencode:${SID}`];
    act(() => sse.emitOce(busy));
    expect(useApiStore.getState().recentStatusRevisions[`opencode:${SID}`]).toBeGreaterThan(revision);
    unmount();
  });

  it('mirrors a fresh detail status but does not publish stale cached status while loading', async () => {
    const detail = makeDetail();
    const busyDetail = { ...detail, session: { ...detail.session, status: 'busy' as const } };
    useApiStore.getState().setCachedSession(SID, busyDetail);
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [detail.session]);
    let finish!: (detail: SessionDetail) => void;
    const fetchSession = vi.fn().mockReturnValue(new Promise<SessionDetail>((resolve) => { finish = resolve; }));
    const view = renderHook(() => useSession(SID, { fetchSession }));
    expect(view.result.current.session?.status).toBe('busy');
    expect(getSidebarSessions()[0].status).toBe('done');
    await act(async () => finish(busyDetail));
    expect(getSidebarSessions()[0].status).toBe('busy');
    fetchSession.mockResolvedValue(busyDetail);
    await act(async () => FakeEventSource.latest()!.emitOce(turnEnded()));
    // The reconciled REST snapshot remains authoritative.
    expect(getSidebarSessions()[0].status).toBe('busy');
    fetchSession.mockResolvedValue(detail);
    await act(async () => { await view.result.current.refresh(); });
    expect(getSidebarSessions()[0].status).toBe('done');
    view.unmount();
  });

  it.each(['status', 'hello', 'idle'])('does not publish cached status after refresh failure, but accepts a subsequent live %s event', async (kind) => {
    const detail = makeDetail();
    useApiStore.getState().setCachedSession(SID, { ...detail, session: { ...detail.session, status: 'busy' } });
    queryClient.setQueryData(SIDEBAR_SESSIONS_KEY, [detail.session]);
    const view = renderHook(() => useSession(SID, { fetchSession: vi.fn().mockRejectedValue(new Error('offline')) }));
    await waitFor(() => expect(view.result.current.refreshError).toBe('offline'));
    expect(view.result.current.session?.status).toBe('busy');
    expect(getSidebarSessions()[0].status).toBe('done');
    const sse = FakeEventSource.latest()!;
    act(() => {
      sse.emitOce(busy, { sessionId: 'other' });
      sse.emitNamed('oce', { ...oceEnvelope(SID, busy), event: { type: 'future.event' } });
    });
    expect(getSidebarSessions()[0].status).toBe('done');
    const revision = useApiStore.getState().recentStatusRevisions[`opencode:${SID}`] ?? 0;
    const events: Record<string, OceEvent> = {
      status: busy,
      hello: hello(false, { status: busy as Extract<OceEvent, { type: 'session.status' }>, prompts: [], judge: [],
        background: { type: 'background.changed', count: 0, tasks: [] }, inflight: { messages: [], parts: [] }, liveConnection: true }),
      idle: idleEdge,
    };
    await act(async () => sse.emitOce(events[kind]));
    await waitFor(() => expect(getSidebarSessions()[0].status).toBe(kind === 'idle' ? 'done' : 'busy'));
    expect(useApiStore.getState().recentStatusRevisions[`opencode:${SID}`]).toBeGreaterThan(revision);
    view.unmount();
  });

  it('reduces an ordered burst once per frame, with a 50ms background fallback', async () => {
    const { result, sse, frame, unmount } = await setup();
    const before = result.current;
    act(() => sse.emitOce(delta(0, 'a')));
    act(() => sse.emitOce(delta(1, 'b')));
    expect(result.current).toBe(before);
    frame();
    expect(textOf(result.current.parts)).toBe('ab');
    act(() => sse.emitOce(delta(2, 'c')));
    act(() => vi.advanceTimersByTime(50));
    expect(textOf(result.current.parts)).toBe('abc');
    frame();
    expect(textOf(result.current.parts)).toBe('abc');
    unmount();
  });

  it('bumps the changes tick for a dirty signal without refetching the transcript', async () => {
    const { result, fetchSession, sse, unmount } = await setup();
    const before = result.current.changesDirtyTick;
    act(() => sse.emitOce({ type: 'changes.dirty', reason: 'edit' }));
    await act(async () => vi.advanceTimersByTime(1000));
    expect(result.current.changesDirtyTick).toBe(before + 1);
    expect(fetchSession).toHaveBeenCalledTimes(1);
    unmount();
  });

  it('flushes final deltas and reconciles once per settled turn end', async () => {
    const { result, fetchSession, sse, unmount } = await setup();
    act(() => sse.emitOce(delta(0, 'final')));
    act(() => sse.emitOce(idleEdge));
    expect(textOf(result.current.parts)).toBe('final');
    await act(async () => sse.emitOce(turnEnded()));
    expect(fetchSession).toHaveBeenCalledTimes(2);
    // The settled turn end cancels the provisional fallback.
    await act(async () => vi.advanceTimersByTime(SETTLE_RECONCILE_MS));
    expect(fetchSession).toHaveBeenCalledTimes(2);
    act(() => sse.emitOce(busy));
    await act(async () => sse.emitOce(turnEnded()));
    expect(fetchSession).toHaveBeenCalledTimes(3);
    unmount();
  });

  it('reconciles once when an idle edge is never followed by a turn end', async () => {
    const { fetchSession, sse, unmount } = await setup();
    act(() => sse.emitOce(idleEdge));
    await act(async () => vi.advanceTimersByTime(SETTLE_RECONCILE_MS - 1));
    expect(fetchSession).toHaveBeenCalledTimes(1);
    await act(async () => vi.advanceTimersByTime(1));
    expect(fetchSession).toHaveBeenCalledTimes(2);
    unmount();
  });

  it.each([
    [{ type: 'history.rewritten', reason: 'compaction' }, 2],
    [{ type: 'stream.reset', reason: 'source-restarted' }, 2],
    [{ type: 'stream.reset', reason: 'slow-consumer' }, 1],
  ] as const)('reconciles for %j as the design requires', async (event, fetches) => {
    const { fetchSession, sse, unmount } = await setup();
    await act(async () => sse.emitOce(event as OceEvent));
    expect(fetchSession).toHaveBeenCalledTimes(fetches);
    unmount();
  });

  it('reconciles a part the stream could not anchor after a grace period', async () => {
    const { result, fetchSession, sse, frame, unmount } = await setup();
    act(() => sse.emitOce(partUpserted({ id: 'p', messageId: 'm', data: { type: 'text', text: 'ab' } }, SID)));
    act(() => sse.emitOce(delta(5, 'gap')));
    frame();
    expect(textOf(result.current.parts)).toBe('ab');
    expect(result.current._stale?.has('p')).toBe(true);
    await act(async () => vi.advanceTimersByTime(STALE_RECONCILE_MS));
    expect(fetchSession).toHaveBeenCalledTimes(2);
    expect(result.current._stale?.size ?? 0).toBe(0);
    unmount();
  });

  it('does not reconcile a gap that a final upsert heals', async () => {
    const { fetchSession, sse, frame, unmount } = await setup();
    act(() => sse.emitOce(partUpserted({ id: 'p', messageId: 'm', data: { type: 'text', text: 'ab' } }, SID)));
    act(() => sse.emitOce(delta(5, 'gap')));
    act(() => sse.emitOce(partUpserted({ id: 'p', messageId: 'm', data: { type: 'text', text: 'abcdegap' } }, SID, true)));
    frame();
    await act(async () => vi.advanceTimersByTime(STALE_RECONCILE_MS));
    expect(fetchSession).toHaveBeenCalledTimes(1);
    unmount();
  });

  it('keeps queued deltas in the outgoing cache and cancels delayed work on navigation', async () => {
    const { result, fetchSession, sse, rerender, unmount } = await setup();
    act(() => sse.emitOce(delta(0, 'keep')));
    act(() => sse.emitOce(idleEdge));
    act(() => sse.emitOce(delta(4, '!')));
    await act(async () => rerender({ id: 'other' }));
    expect(textOf(useApiStore.getState().getCachedSession(SID)!.parts)).toBe('keep!');
    await act(async () => vi.advanceTimersByTime(SETTLE_RECONCILE_MS));
    expect(fetchSession).toHaveBeenCalledTimes(2);
    expect(result.current.sessionId).toBe('other');
    expect(result.current.parts).toEqual([]);
    unmount();
  });

  it('delivers urgent permissions immediately after earlier queued content', async () => {
    const { result, sse, frame, unmount } = await setup();
    act(() => sse.emitOce(delta(0, 'before prompt')));
    act(() => sse.emitOce({ type: 'prompt.asked', prompt: {
      id: 'perm', kind: 'permission', sessionId: SID, askedAt: 1, permission: { permission: 'bash', patterns: ['ls'] },
    } }));
    expect(result.current.pendingPermission?.permissionId).toBe('perm');
    expect(textOf(result.current.parts)).toBe('before prompt');
    frame();
    expect(textOf(result.current.parts)).toBe('before prompt');
    unmount();
  });

  it('routes queue changes to the queue listeners', async () => {
    const { sse, unmount } = await setup();
    const seen: unknown[] = [];
    const stop = onQueueUpdated((sid, messages) => seen.push([sid, messages]));
    const queued = [{ id: 'q1', text: 'later' }] as never;
    act(() => sse.emitOce({ type: 'queue.changed', messages: queued }));
    expect(seen).toEqual([[SID, queued]]);
    stop();
    unmount();
  });

  it('preserves snapshot/delta ordering and flushes before applying a REST snapshot', async () => {
    const { result, sse, frame, fetchSession, unmount } = await setup();
    act(() => sse.emitOce(partUpserted({ id: 'p', messageId: 'm', data: { type: 'text', text: 'a' } }, SID)));
    act(() => sse.emitOce(delta(1, 'b')));
    act(() => sse.emitOce(partUpserted({ id: 'p', messageId: 'm', data: { type: 'text', text: 'ab' } }, SID)));
    frame();
    expect(textOf(result.current.parts)).toBe('ab');

    let resolve!: (detail: SessionDetail) => void;
    fetchSession.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    act(() => sse.emitOce(turnEnded()));
    act(() => sse.emitOce(delta(2, 'c')));
    await act(async () => resolve(makeDetail({
      parts: [{ id: 'p', messageId: 'm', sessionId: SID, data: { type: 'text', text: 'abc' } }],
    })));
    frame();
    expect(textOf(result.current.parts)).toBe('abc');
    unmount();
  });

  it('ignores turn ends addressed to another session', async () => {
    const { fetchSession, sse, unmount } = await setup();
    await act(async () => sse.emitOce(turnEnded(), { sessionId: 'child' }));
    expect(fetchSession).toHaveBeenCalledTimes(1);
    unmount();
  });

  it('allows the next turn end to retry a failed reconcile', async () => {
    const { fetchSession, sse, unmount } = await setup();
    fetchSession.mockRejectedValueOnce(new Error('offline'));
    await act(async () => sse.emitOce(turnEnded()));
    await act(async () => sse.emitOce(turnEnded()));
    expect(fetchSession).toHaveBeenCalledTimes(3);
    unmount();
  });

  it('settles a failed turn from the stream before the reconcile lands', async () => {
    const { result, fetchSession, sse, unmount } = await setup();
    const detail = makeDetail();
    fetchSession.mockResolvedValueOnce({ ...detail, session: { ...detail.session, status: 'error' } });
    act(() => sse.emitOce(idleEdge));
    expect(result.current.session?.status).toBe('done');
    act(() => sse.emitOce({ type: 'session.status', status: 'error' }));
    expect(result.current.session?.status).toBe('error');
    await act(async () => sse.emitOce(turnEnded('error')));
    expect(result.current.session?.status).toBe('error');
    expect(fetchSession).toHaveBeenCalledTimes(2);
    unmount();
  });

  it('reconciles after a reconnect the owner could not resume, but not after a resumed one', async () => {
    const { result, fetchSession, sse, unmount } = await setup();
    await act(async () => { sse.open(); sse.emitOce(hello(false), { epoch: 'e1', seq: 7 }); });
    expect(fetchSession).toHaveBeenCalledTimes(2);
    act(() => sse.error());
    act(() => result.current.retryNow());
    const resumed = FakeEventSource.latest()!;
    expect(resumed.url).toContain('after=e1%3A7');
    await act(async () => { resumed.open(); resumed.emitOce(hello(true), { epoch: 'e1', seq: 8 }); });
    expect(fetchSession).toHaveBeenCalledTimes(2);
    act(() => resumed.error());
    act(() => result.current.retryNow());
    const fresh = FakeEventSource.latest()!;
    expect(fresh.url).toContain('after=e1%3A8');
    await act(async () => { fresh.open(); fresh.emitOce(hello(false), { epoch: 'e2', seq: 1 }); });
    expect(fetchSession).toHaveBeenCalledTimes(3);
    unmount();
  });

  it('loads a turn completed between the initial REST snapshot and first hello', async () => {
    const { result, fetchSession, sse, unmount } = await setup();
    expect(result.current.messages.some((m) => m.id === 'between')).toBe(false);
    fetchSession.mockResolvedValueOnce(makeDetail({ messages: [{ id: 'between', sessionId: SID,
      timeCreated: 123, data: { role: 'assistant', finish: 'stop' } }], parts: [] }));
    await act(async () => { sse.open(); sse.emitOce(hello(false)); });
    expect(result.current.messages.map((m) => m.id)).toEqual(['between']);
    unmount();
  });

  it('keeps the final response when turn end overtakes reconnect recovery before history catches up', async () => {
    const { result, fetchSession, sse, unmount } = await setup();
    let finishRecovery!: (detail: SessionDetail) => void;
    fetchSession.mockImplementationOnce(() => new Promise<SessionDetail>((resolve) => { finishRecovery = resolve; }));
    act(() => sse.emitOce(hello(false)));
    act(() => {
      sse.emitOce({ type: 'message.upserted', message: { id: 'final', sessionId: SID, timeCreated: 3, data: { role: 'assistant' } } });
      sse.emitOce(partUpserted({ id: 'final-part', messageId: 'final', data: { type: 'text', text: 'Final response' } }, SID, true));
      sse.emitOce(idleEdge);
    });
    expect(result.current.messages.map((m) => m.id)).toContain('final');
    await act(async () => sse.emitOce(turnEnded()));
    await act(async () => finishRecovery(makeDetail()));
    expect(result.current.messages.map((m) => m.id)).toContain('final');
    expect(result.current.parts.find((p) => p.id === 'final-part')?.data).toEqual({ type: 'text', text: 'Final response' });
    expect(result.current.refreshError).toBeNull();
    unmount();
  });

  describe('composed reads and their stream cursor', () => {
    const msg = (data: Record<string, unknown>) => ({ id: 'm', sessionId: SID, timeCreated: 2, data: { role: 'assistant', ...data } }) as SessionDetail['messages'][number];
    const composed = (cursor: string) => makeDetail({
      messages: [msg({ cost: 5 })],
      parts: [{ id: 'p', messageId: 'm', sessionId: SID, data: JSON.stringify({ type: 'text', text: 'Hello' }) }],
      stream: { cursor },
    });

    it('recovers from a read and replays only the events after its cursor', async () => {
      const { result, sse, fetchSession, unmount } = await setup();
      let finish!: (d: SessionDetail) => void;
      fetchSession.mockImplementationOnce(() => new Promise<SessionDetail>((resolve) => { finish = resolve; }));
      act(() => sse.emitOce(hello(false), { epoch: 'e1', seq: 1 }));
      act(() => {
        // Covered by the read, which also holds history's metadata.
        sse.emitOce({ type: 'message.upserted', message: msg({}) }, { epoch: 'e1', seq: 2 });
        sse.emitOce(partUpserted({ id: 'p', messageId: 'm', data: { type: 'text', text: 'Hello' } }, SID), { epoch: 'e1', seq: 3 });
        // Newer than the read.
        sse.emitOce(delta(5, ' world'), { epoch: 'e1', seq: 4 });
      });
      await act(async () => finish(composed('e1:3')));
      expect(result.current.messages[0].data.cost).toBe(5);
      expect(textOf(result.current.parts)).toBe('Hello world');
      unmount();
    });

    it('keeps metadata events the read does not compose, even before its cursor', async () => {
      const { result, sse, fetchSession, unmount } = await setup();
      let finish!: (d: SessionDetail) => void;
      fetchSession.mockImplementationOnce(() => new Promise<SessionDetail>((resolve) => { finish = resolve; }));
      act(() => sse.emitOce(hello(false), { epoch: 'e1', seq: 1 }));
      act(() => sse.emitOce({ type: 'session.updated', patch: { title: 'Renamed' } }, { epoch: 'e1', seq: 2 }));
      await act(async () => finish(composed('e1:2')));
      expect(result.current.session?.title).toBe('Renamed');
      unmount();
    });

    it('lets events after a reconcile read win over it', async () => {
      const { result, sse, fetchSession, unmount } = await setup();
      let finish!: (d: SessionDetail) => void;
      fetchSession.mockImplementationOnce(() => new Promise<SessionDetail>((resolve) => { finish = resolve; }));
      await act(async () => sse.emitOce(turnEnded(), { epoch: 'e1', seq: 1 }));
      act(() => {
        sse.emitOce({ type: 'message.upserted', message: msg({ cost: 5 }) }, { epoch: 'e1', seq: 2 });
        sse.emitOce({ type: 'message.upserted', message: msg({ cost: 5, time: { created: 2, completed: 9 } }) }, { epoch: 'e1', seq: 3 });
      });
      await act(async () => finish(composed('e1:2')));
      expect(result.current.messages[0].data.time?.completed).toBe(9);
      expect(textOf(result.current.parts)).toBe('Hello');
      unmount();
    });

    it('rereads stored history when it commits while settled, not during a turn', async () => {
      const { result, sse, fetchSession, unmount } = await setup();
      const committed: OceEvent = { type: 'history.committed', revision: 3 };
      act(() => { sse.emitOce(busy); sse.emitOce(committed); });
      await act(async () => vi.advanceTimersByTimeAsync(COMMIT_READ_DELAY_MS));
      expect(fetchSession).toHaveBeenCalledTimes(1);
      fetchSession.mockResolvedValue(composed('e9:1'));
      act(() => { sse.emitOce({ type: 'session.status', status: 'done' }); sse.emitOce(committed); sse.emitOce(committed); });
      await act(async () => vi.advanceTimersByTimeAsync(COMMIT_READ_DELAY_MS));
      expect(fetchSession).toHaveBeenCalledTimes(2);
      // Content the stream never delivered arrives from history.
      expect(textOf(result.current.parts)).toBe('Hello');
      unmount();
    });
  });

  it.each(['rewrite', 'cold reconnect'])('%s removes missed deletions but keeps events received after the request fence', async (trigger) => {
    const { result, fetchSession, sse, unmount } = await setup();
    await act(async () => { sse.open(); sse.emitOce(hello(false)); sse.emitOce(partUpserted({ id: 'p', messageId: 'm', data: { type: 'text', text: 'removed' } }, SID)); });
    act(() => sse.emitOce({ type: 'session.status', status: 'busy' }));
    expect(result.current.parts.some((p) => p.id === 'p')).toBe(true);
    let finish!: (d: SessionDetail) => void;
    fetchSession.mockImplementationOnce(() => new Promise<SessionDetail>((resolve) => { finish = resolve; }));
    await act(async () => {
      if (trigger === 'rewrite') sse.emitOce({ type: 'history.rewritten', reason: 'revert' });
      else { sse.error(); result.current.retryNow(); FakeEventSource.latest()!.open(); FakeEventSource.latest()!.emitOce(hello(false)); }
    });
    const active = FakeEventSource.latest()!;
    act(() => { active.emitOce(partUpserted({ id: 'new', messageId: 'new-m', data: { type: 'text', text: 'new turn' } }, SID)); active.emitOce({ type: 'session.status', status: 'busy' }); });
    await act(async () => finish(makeDetail({ messages: [], parts: [] })));
    expect(result.current.parts.some((p) => p.id === 'p')).toBe(false);
    expect(result.current.parts.some((p) => p.id === 'new')).toBe(true);
    expect(result.current.session?.status).toBe('busy');
    unmount();
  });

  it('shows upstream failures even when EventSource remains open', async () => {
    const { result, sse, unmount } = await setup();
    act(() => { sse.open(); sse.emitOce({ type: 'stream.error', code: 'owner-offline', message: 'Disconnected owner', retryable: true }); });
    expect(result.current.status).toBe('reconnecting');
    expect(result.current.sseReconnecting).toBe(true);
    act(() => sse.emitOce(hello(false)));
    expect(result.current.status).toBe('live');
    expect(result.current.sseReconnecting).toBe(false);
    unmount();
  });

  it('flushes on disconnect and preserves the unmount cache without delayed callbacks', async () => {
    const { result, fetchSession, sse, frame, unmount } = await setup();
    act(() => sse.emitOce(delta(0, 'a')));
    act(() => sse.error());
    expect(textOf(result.current.parts)).toBe('a');
    // A reconnect can still be scheduled when the user leaves.
    unmount();
    act(() => sse.emitOce(delta(1, 'late')));
    frame();
    await act(async () => vi.advanceTimersByTime(1000));
    expect(textOf(useApiStore.getState().getCachedSession(SID)!.parts)).toBe('a');
    expect(fetchSession).toHaveBeenCalledTimes(1);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it('batches the debug ring and coalesces resource dirtiness', async () => {
    const { result, sse, frame, unmount } = await setup(true);
    act(() => {
      sse.emitNamed('oce', ' ');
      sse.emitNamed('oce', '{bad json');
      for (let i = 0; i < 60; i++) sse.emitOce(delta(i, 'x'));
      sse.emitOce({ type: 'changes.dirty', reason: 'edit' });
      sse.emitOce({ type: 'changes.dirty', reason: 'commit' });
    });
    frame();
    expect(result.current.sseDebugEvents).toHaveLength(50);
    expect(result.current.changesDirtyTick).toBe(1);
    expect(textOf(result.current.parts)).toBe('x'.repeat(60));
    unmount();
  });
});

describe('useSession — initial load', () => {
  it('fetches /api/session/{id} on mount and dispatches load', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);

    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    // Loading state appears immediately.
    expect(result.current.status).toBe('loading');

    await waitFor(() => {
      expect(result.current.session?.id).toBe(SID);
    });
    expect(fetchSession).toHaveBeenCalledWith(SID, expect.any(Number), 0, expect.anything(), undefined, false);
    expect(result.current.status).toBe('live');
  });

  it('preserves the session tree in the authoritative cache entry', async () => {
    const child = { ...makeDetail().session, id: 'sess-child', parentId: SID };
    const detail = makeDetail({ sessionTree: [makeDetail().session, child] });

    const { result } = renderHook(() => useSession(SID, {
      fetchSession: vi.fn().mockResolvedValue(detail),
    }));

    await waitFor(() => expect(result.current.session?.id).toBe(SID));
    expect(useApiStore.getState().getCachedSession(SID)?.sessionTree).toEqual(detail.sessionTree);
  });

  it('retains a cached tree when navigation detail refetch fails', async () => {
    const targetId = 'sess-child';
    const parent = { ...makeDetail().session, title: 'Parent session' };
    const target = {
      ...makeDetail().session,
      id: targetId,
      parentId: SID,
      totalInputTokens: 30,
      totalOutputTokens: 40,
      totalCost: 0.2,
    };
    const descendant = {
      ...makeDetail().session,
      id: 'sess-grandchild',
      parentId: targetId,
      totalInputTokens: 50,
      totalOutputTokens: 60,
      totalCost: 0.3,
    };
    const cachedTarget = makeDetail({
      session: target,
      sessionTree: [parent, target, descendant],
    });
    useApiStore.getState().setCachedSession(targetId, cachedTarget);
    const fetchSession = vi.fn().mockImplementation((id: string) => (
      id === SID ? Promise.resolve(makeDetail()) : Promise.reject(new Error('refetch failed'))
    ));
    const { result, rerender } = renderHook(
      ({ id }) => useSession(id, { fetchSession }),
      { initialProps: { id: SID } },
    );
    await waitFor(() => expect(result.current.session?.id).toBe(SID));

    rerender({ id: targetId });
    await waitFor(() => expect(result.current.refreshError).toBe('refetch failed'));

    // The cache stays on screen: the failure is a refresh error, not a load error.
    expect(result.current.loadError).toBeNull();
    expect(result.current.refreshing).toBe(false);
    expect(result.current.status).not.toBe('error');
    expect(result.current.session?.id).toBe(targetId);
    expect(result.current.sessionTree.find((session) => session.id === SID)?.title).toBe('Parent session');
    expect(aggregateSessionTreeStats(target, result.current.sessionTree, {
      input: target.totalInputTokens,
      output: target.totalOutputTokens,
      reasoning: 0,
      cacheRead: 0,
      cacheWrite: 0,
      totalCost: target.totalCost,
    })).toEqual({ input: 80, output: 100, totalCost: 0.5, totalEstCost: 0, totalEffectiveCost: 0.5, sessions: 2 });
  });

  it('flags a cached render as refreshing until the refresh lands, and on reload', async () => {
    useApiStore.getState().setCachedSession(SID, makeDetail());
    let resolve: (d: SessionDetail) => void = () => {};
    const fetchSession = vi.fn(() => new Promise<SessionDetail>((r) => { resolve = r; }));
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    expect(result.current.session?.id).toBe(SID);
    expect(result.current.loading).toBe(false);
    expect(result.current.refreshing).toBe(true);
    await act(async () => resolve(makeDetail()));
    expect(result.current.refreshing).toBe(false);

    let reload: Promise<void> = Promise.resolve();
    act(() => { reload = result.current.reload(); });
    expect(result.current.refreshing).toBe(true);
    await act(async () => { resolve(makeDetail()); await reload; });
    expect(result.current.refreshing).toBe(false);
  });

  it('retries a failed refresh without discarding live content or prompts', async () => {
    useApiStore.getState().setCachedSession(SID, makeDetail());
    const fetchSession = vi.fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue(makeDetail()); // older snapshot: lacks the streamed part
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(result.current.refreshError).toBe('offline'));

    const sse = FakeEventSource.latest()!;
    act(() => sse.emitOce(textDelta('p', 0, 'live')));
    act(() => sse.emitOce({ type: 'prompt.asked', prompt: {
      id: 'q1', kind: 'question', sessionId: SID, askedAt: 1,
      question: { questions: [{ question: 'ok?', header: 'h', options: [] }] },
    } }));
    await waitFor(() => expect(result.current.parts.some((p) => p.id === 'p')).toBe(true));
    expect(result.current.pendingQuestion).not.toBeNull();

    await act(async () => { await result.current.refresh(); });
    expect(result.current.refreshError).toBeNull();
    expect(result.current.parts.some((p) => p.id === 'p')).toBe(true);
    expect(result.current.pendingQuestion).not.toBeNull();
  });

  it('keeps a remote session on its owner when prefetches evict its cache entry', async () => {
    const remote = makeDetail({ session: { ...makeDetail().session, platform: 'r-m2:opencode' } });
    useApiStore.getState().setCachedSession(SID, remote);
    const fetchSession = vi.fn().mockResolvedValue(remote);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(result.current.refreshing).toBe(false));
    const sse = FakeEventSource.latest()!;
    expect(sse.url).toContain('platform=r-m2');
    act(() => sse.emitOce(textDelta('p', 0, 'live')));
    await waitFor(() => expect(result.current.parts.some((p) => p.id === 'p')).toBe(true));

    // Ten hover prefetches push the active entry out of the LRU.
    for (let i = 0; i < 10; i++) useApiStore.getState().setCachedSession(`other-${i}`, makeDetail());
    expect(useApiStore.getState().getCachedSession(SID)).toBeNull();
    act(() => sse.emitOce(textDelta('p', 4, '!')));

    const text = () => {
      const data = result.current.parts.find((p) => p.id === 'p')?.data;
      return (typeof data === 'string' ? JSON.parse(data) : data)?.text;
    };
    await waitFor(() => expect(text()).toBe('live!'));
    await act(async () => {});
    expect(text()).toBe('live!');
    expect(FakeEventSource.latest()).toBe(sse);
    expect(sse.closed).toBe(false);
    expect(fetchSession).toHaveBeenCalledTimes(1);
    expect(fetchSession).toHaveBeenLastCalledWith(SID, expect.any(Number), 0, expect.anything(), 'r-m2:opencode', false);
  });

  it('caches only the newest page of a long view', async () => {
    const messages = Array.from({ length: 5 }, (_, i) => ({ id: `m${i}`, sessionId: SID, timeCreated: i, data: { role: 'user' as const } }));
    const parts = messages.map((m) => ({ id: `${m.id}-p`, messageId: m.id, sessionId: SID, data: { type: 'text', text: m.id } }));
    useApiStore.getState().setCachedSession(SID, makeDetail());
    const { result, unmount } = renderHook(() => useSession(SID, {
      fetchSession: vi.fn().mockResolvedValue(makeDetail({ messages, parts, totalMessages: 5 })),
      pageSize: 2,
    }));
    await waitFor(() => expect(result.current.messages).toHaveLength(5));
    unmount();
    const cached = useApiStore.getState().getCachedSession(SID)!;
    expect(cached.messages.map((m) => m.id)).toEqual(['m3', 'm4']);
    expect(cached.parts.map((p) => p.messageId)).toEqual(['m3', 'm4']);
    expect(cached.totalMessages).toBe(5);
  });

  it('clears a refresh error once a retry succeeds', async () => {
    useApiStore.getState().setCachedSession(SID, makeDetail());
    const fetchSession = vi.fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue(makeDetail());
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(result.current.refreshError).toBe('offline'));

    await act(async () => { await result.current.reload(); });
    expect(result.current.refreshError).toBeNull();
    expect(result.current.session?.id).toBe(SID);
  });

  it('hydrates fetched history so an unloaded message can be scrolled to', async () => {
    const detail = makeDetail({
      messages: [{ id: 'new', sessionId: SID, timeCreated: 2_000, data: { role: 'user' } }],
    });
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(result.current.session?.id).toBe(SID));

    act(() => result.current.hydrateHistory(
      [{ id: 'old', sessionId: SID, timeCreated: 1_000, data: { role: 'user' } }, ...detail.messages],
      [{ id: 'old-part', messageId: 'old', sessionId: SID, data: { type: 'text', text: 'Older prompt' } }],
    ));

    expect(result.current.messages.map((message) => message.id)).toEqual(['old', 'new']);
    expect(result.current.parts[0]?.messageId).toBe('old');
  });

  it('keeps live messages when fetched jump history is hydrated', async () => {
    const kept = { id: 'kept', sessionId: SID, timeCreated: 1_000, data: { role: 'assistant' as const } };
    const live = { id: 'live', sessionId: SID, timeCreated: 2_000, data: { role: 'assistant' as const } };
    const livePart = { id: 'live-part', messageId: 'live', sessionId: SID, data: { type: 'text', text: 'live result' } };
    const detail = makeDetail({ messages: [kept, live], parts: [livePart] });
    const { result } = renderHook(() => useSession(SID, { fetchSession: vi.fn().mockResolvedValue(detail) }));
    await waitFor(() => expect(result.current.session?.id).toBe(SID));

    act(() => result.current.hydrateHistory(
      [{ ...kept, data: { role: 'assistant', finish: 'stale' } }, { id: 'middle', sessionId: SID, timeCreated: 1_500, data: { role: 'user' } }, { ...live, data: { role: 'assistant', finish: 'stale' } }],
      [{ id: 'old-part', messageId: 'old', sessionId: SID, data: { type: 'text', text: 'old' } }, { ...livePart, data: { type: 'text', text: 'stale' } }],
    ));

    expect(result.current.messages.map((message) => message.id)).toEqual(['kept', 'middle', 'live']);
    expect(result.current.messages[0].data).not.toHaveProperty('finish');
    expect(result.current.messages[2].data).not.toHaveProperty('finish');
    expect(result.current.parts.find((part) => part.id === 'live-part')?.data).toEqual(livePart.data);
  });

  it('retains a protected jump target when hydrated history exceeds the memory limit', async () => {
    const detail = makeDetail({ messages: [{ id: 'latest', sessionId: SID, timeCreated: 300, data: { role: 'assistant' } }] });
    const { result } = renderHook(() => useSession(SID, {
      fetchSession: vi.fn().mockResolvedValue(detail),
      maxMessages: 200,
      trimTo: 150,
      protectedMessageId: 'oldest',
    }));
    await waitFor(() => expect(result.current.session?.id).toBe(SID));
    const history = Array.from({ length: 201 }, (_, index) => ({
      id: index === 0 ? 'oldest' : `m-${index}`,
      sessionId: SID,
      timeCreated: index,
      data: { role: 'assistant' as const },
    }));

    act(() => result.current.hydrateHistory(history, []));

    await waitFor(() => expect(result.current.messages).toHaveLength(150));
    expect(result.current.messages.some((message) => message.id === 'oldest')).toBe(true);
  });

  it('skips when id is undefined', () => {
    const fetchSession = vi.fn();
    const { result } = renderHook(() => useSession(undefined, { fetchSession }));
    expect(fetchSession).not.toHaveBeenCalled();
    expect(result.current.status).toBe('loading');
  });

  it('does not fetch for the `new` sentinel id and reports an empty live view', async () => {
    const fetchSession = vi.fn();
    const { result } = renderHook(() => useSession('new', { fetchSession }));
    expect(fetchSession).not.toHaveBeenCalled();
    expect(result.current.status).toBe('live');
    expect(result.current.loading).toBe(false);
    expect(result.current.loadError).toBeNull();
    expect(result.current.session).toBeNull();
  });

  it('exposes error status when fetch fails', async () => {
    const fetchSession = vi.fn().mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => {
      expect(result.current.status).toBe('error');
    });
  });

  it.each([
    ['error', 'error', 'connection refused'],
    ['busy', 'rate_limit', "This request would exceed your account's rate limit. Please try again later."],
  ] as const)('adds %s session %s notices to the message history', async (status, kind, message) => {
    const detail = makeDetail({
      session: {
        ...makeDetail().session,
        status,
        notice: { kind, message, retryAt: 0, attempt: 0 },
      },
    });
    const fetchSession = vi.fn().mockResolvedValue(detail);

    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await waitFor(() => {
      expect(result.current.messages.some((m) => m.id === `ocman-session-notice-${SID}`)).toBe(true);
    });
    expect(result.current.parts.find((p) => p.messageId === `ocman-session-notice-${SID}`)?.data).toEqual({
      type: 'text',
      text: message,
    });
  });
});

describe('useSession — conversation stream dispatch', () => {
  async function connected() {
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const hook = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(hook.result.current.session?.id).toBe(SID));
    const sse = FakeEventSource.latest()!;
    act(() => { sse.open(); sse.emitOce(hello()); });
    return { ...hook, sse, fetchSession };
  }
  const dataOf = (part?: { data: unknown }) => (typeof part?.data === 'string' ? JSON.parse(part.data) : part?.data);

  it('routes message and part upserts through the reducer', async () => {
    const { result, sse } = await connected();
    act(() => {
      sse.emitOce({ type: 'message.upserted', message: { id: 'm-1', sessionId: SID, timeCreated: 100, data: { role: 'assistant' } } });
      sse.emitOce(partUpserted({ id: 'p-1', messageId: 'm-1', data: { type: 'text', text: 'Hi' } }, SID, true));
    });
    await waitFor(() => expect(result.current.messages.find((m) => m.id === 'm-1')).toBeDefined());
    expect(dataOf(result.current.parts.find((p) => p.id === 'p-1')).text).toBe('Hi');
  });

  it('applies a replayed delta once', async () => {
    const { result, sse } = await connected();
    act(() => {
      sse.emitOce(textDelta('p-dup', 0, 'one ', 'm-dup'));
      sse.emitOce(textDelta('p-dup', 0, 'one ', 'm-dup'));
    });
    await waitFor(() => expect(result.current.parts.find((p) => p.id === 'p-dup')).toBeDefined());
    expect(dataOf(result.current.parts.find((p) => p.id === 'p-dup')).text).toBe('one ');
  });

  it('ignores legacy channels on the conversation stream', async () => {
    const { result, sse } = await connected();
    act(() => {
      sse.emitMessage({ type: 'message.part.delta', properties: { partID: 'p', messageID: 'm', sessionID: SID, field: 'text', delta: 'x' } });
      sse.emitNamed('message.part.delta', { properties: { partID: 'p', messageID: 'm', sessionID: SID, field: 'text', delta: 'x' } });
    });
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 60)); });
    expect(result.current.parts).toEqual([]);
  });

  it('shows a running tool part with a stub message before its message arrives', async () => {
    const { result, sse } = await connected();
    act(() => sse.emitOce(partUpserted({ id: 'p-tool-live', messageId: 'm-tool-live', data: {
      type: 'tool', tool: 'bash', state: { status: 'running', input: { command: 'sleep 10' } },
    } }, SID)));
    await waitFor(() => expect(result.current.messages.find((m) => m.id === 'm-tool-live')).toBeDefined());
    const data = dataOf(result.current.parts.find((p) => p.id === 'p-tool-live'));
    expect(data).toMatchObject({ type: 'tool', tool: 'bash', state: { status: 'running' } });
  });

  it('dirties session info after the owner confirms persisted changes', async () => {
    const { result, sse } = await connected();
    const before = result.current.changesDirtyTick;
    act(() => sse.emitOce({ type: 'changes.dirty', reason: 'commit' }));
    await waitFor(() => expect(result.current.changesDirtyTick).toBe(before + 1));
  });

  it('opens the conversation stream against the correct URL', async () => {
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(FakeEventSource.latest()).toBeDefined());
    expect(FakeEventSource.latest()!.url).toBe(`/api/session/${SID}/stream`);
  });

  it('routes remote streams and fetches through the cached platform', async () => {
    const detail = makeDetail({ session: { ...makeDetail().session, platform: 'r-box:opencode' } });
    useApiStore.getState().setCachedSession(SID, detail);
    const fetchSession = vi.fn().mockResolvedValue(detail);
    renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(FakeEventSource.latest()).toBeDefined());
    expect(FakeEventSource.latest()!.url).toContain(`/api/session/${SID}/stream?platform=r-box%3Aopencode`);
    expect(fetchSession).toHaveBeenCalledWith(SID, expect.any(Number), 0, expect.any(AbortSignal), 'r-box:opencode', false);
  });
});

describe('useSession — returning to the tab', () => {
  let hidden = false;
  beforeEach(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
  });
  afterEach(() => { hidden = false; });

  async function awayFor(ms: number) {
    hidden = true;
    act(() => { document.dispatchEvent(new Event('visibilitychange')); });
    await act(() => vi.advanceTimersByTimeAsync(ms));
    hidden = false;
    act(() => { document.dispatchEvent(new Event('visibilitychange')); });
  }

  // Sleep can leave the stream half-open: no error fires, the turn's
  // events never arrive, and the conversation stays frozen.
  it('replaces a silently dead stream and refetches after a long absence', async () => {
    vi.useFakeTimers();
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    renderHook(() => useSession(SID, { fetchSession, reconnectDelay: () => 10 }));
    await vi.waitFor(() => expect(fetchSession).toHaveBeenCalledTimes(1));
    const first = FakeEventSource.latest()!;
    act(() => { first.open(); first.emitNamed('ocman.keepalive', {}); });

    // Shorter than the keepalive watchdog, so only the resume signal acts.
    await awayFor(KEEPALIVE_TIMEOUT_MS - 1);

    expect(first.closed).toBe(true);
    expect(FakeEventSource.instances).toHaveLength(2);
    await act(async () => { FakeEventSource.latest()!.open(); FakeEventSource.latest()!.emitOce(hello(false)); });
    expect(fetchSession).toHaveBeenCalledTimes(2);
  });

  it('keeps the stream across a brief tab switch', async () => {
    vi.useFakeTimers();
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    renderHook(() => useSession(SID, { fetchSession, reconnectDelay: () => 10 }));
    await vi.waitFor(() => expect(fetchSession).toHaveBeenCalledTimes(1));
    act(() => FakeEventSource.latest()!.open());

    await awayFor(5_000);

    expect(FakeEventSource.instances).toHaveLength(1);
    expect(fetchSession).toHaveBeenCalledTimes(1);
  });
});

describe('useSession — reconnect after error', () => {
  it('reopens the EventSource and refetches state on reconnect', async () => {
    vi.useFakeTimers();
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() =>
      useSession(SID, { fetchSession, reconnectDelay: () => 10 }),
    );

    // Wait for initial load.
    await vi.waitFor(() => {
      expect(result.current.session?.id).toBe(SID);
    });
    expect(FakeEventSource.instances.length).toBe(1);
    const first = FakeEventSource.latest()!;
    act(() => {
      first.open();
    });
    const dirtyBeforeReconnect = result.current.changesDirtyTick;

    // Disconnect.
    act(() => {
      first.error();
    });
    expect(result.current.status).toBe('reconnecting');

    // Backoff fires → new EventSource opens.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15);
    });
    expect(FakeEventSource.instances.length).toBe(2);

    // The new stream cannot resume → refetch + status becomes 'live' again.
    act(() => {
      FakeEventSource.latest()!.open();
      FakeEventSource.latest()!.emitOce(hello(false));
    });
    expect(result.current.status).toBe('live');
    expect(result.current.changesDirtyTick).toBe(dirtyBeforeReconnect + 1);
    // Two fetches total: initial mount + reconnect reconciliation.
    expect(fetchSession).toHaveBeenCalledTimes(2);
  });

  it('refreshes resources when the first successful open follows an offline start', async () => {
    vi.useFakeTimers();
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, {
      fetchSession,
      reconnectDelay: () => 10,
    }));
    await vi.waitFor(() => expect(FakeEventSource.latest()).toBeDefined());
    const dirtyBeforeReconnect = result.current.changesDirtyTick;

    act(() => FakeEventSource.latest()!.error());
    await act(async () => { await vi.advanceTimersByTimeAsync(15); });
    act(() => { FakeEventSource.latest()!.open(); FakeEventSource.latest()!.emitOce(hello(false)); });

    expect(result.current.changesDirtyTick).toBe(dirtyBeforeReconnect + 1);
  });
});

describe('useSession — session.idle refetch', () => {
  it.each(['reload', 'back_forward'])('preserves an archive on a browser %s but allows later navigation to reopen', async (type) => {
    Object.defineProperty(performance, 'getEntriesByType', { configurable: true, value: vi.fn(() => [{ type, name: `http://localhost/session/${SID}` }]) });
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const { rerender, unmount } = renderHook(({ id }) => useSession(id, { fetchSession }), { initialProps: { id: SID } });
    await waitFor(() => expect(fetchSession).toHaveBeenCalledOnce());
    expect(fetchSession.mock.calls[0][5]).toBe(true);
    await act(async () => rerender({ id: 'other-session' }));
    expect(fetchSession.mock.calls.at(-1)?.[5]).toBe(false);
    await act(async () => rerender({ id: 'new' }));
    await act(async () => rerender({ id: 'other-session' }));
    expect(fetchSession.mock.calls.at(-1)?.[5]).toBe(false);
    unmount();
    Reflect.deleteProperty(performance, 'getEntriesByType');
  });

  it('keeps a session archived elsewhere when an existing tab refreshes or reconnects', async () => {
    vi.useFakeTimers();
    const requests: URL[] = [];
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = new URL(String(input), 'http://localhost');
      requests.push(url);
      const detail = makeDetail();
      detail.session.id = url.pathname.split('/').pop()!;
      return new Response(JSON.stringify(detail), { headers: { 'Content-Type': 'application/json' } });
    });
    const { result, rerender, unmount } = renderHook(({ id }) => useSession(id, { reconnectDelay: () => 10 }), {
      initialProps: { id: SID },
    });
    await act(async () => {});
    expect(requests).toHaveLength(1);
    expect(requests[0].searchParams.has('peek')).toBe(false);
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    // Another client archives the session. This tab has no navigation event.
    act(() => sse.error());
    await act(async () => vi.advanceTimersByTimeAsync(10));
    await act(async () => { FakeEventSource.latest()!.open(); FakeEventSource.latest()!.emitOce(hello(false)); });
    expect(requests).toHaveLength(2);
    expect(requests[1].searchParams.get('peek')).toBe('1');
    await act(async () => FakeEventSource.latest()!.emitOce({ type: 'turn.ended', outcome: 'done', endedAt: 1 }));
    await act(async () => result.current.reload());
    await act(async () => result.current.loadMore());
    expect(requests).toHaveLength(5);
    expect(requests.slice(1).every((url) => url.searchParams.get('peek') === '1')).toBe(true);

    await act(async () => rerender({ id: 'other-session' }));
    expect(requests).toHaveLength(6);
    expect(requests[5].searchParams.has('peek')).toBe(false);
    unmount();
  });

  it('refetches /api/session/{id} when a turn ends', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => {
      expect(result.current.session?.id).toBe(SID);
    });
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    act(() => {
      sse.emitOce({ type: 'turn.ended', outcome: 'done', endedAt: 1 });
    });

    await waitFor(() => {
      expect(fetchSession).toHaveBeenCalledTimes(2);
    });
  });
});

describe('useSession — reload()', () => {
  it('refetches and replaces state when reload() is called', async () => {
    const initial = makeDetail({ messages: [] });
    const reloaded = makeDetail({
      messages: [
        { id: 'm-new', sessionId: SID, timeCreated: 999, data: { role: 'user' } },
      ],
    });
    const fetchSession = vi.fn()
      .mockResolvedValueOnce(initial)
      .mockResolvedValueOnce(reloaded);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => {
      expect(result.current.session?.id).toBe(SID);
    });
    await act(async () => {
      await result.current.reload();
    });
    expect(result.current.messages.map((m) => m.id)).toEqual(['m-new']);
  });
});

describe('useSession — session change', () => {
  it('tears down the old EventSource and opens a fresh one on id change', async () => {
    const fetchSession = vi.fn()
      .mockResolvedValueOnce(makeDetail({ session: { ...makeDetail().session, id: 'sess-a' } }))
      .mockResolvedValueOnce(makeDetail({ session: { ...makeDetail().session, id: 'sess-b' } }));
    const { result, rerender } = renderHook(({ id }) => useSession(id, { fetchSession }), {
      initialProps: { id: 'sess-a' as string | undefined },
    });
    await waitFor(() => {
      expect(result.current.session?.id).toBe('sess-a');
    });
    const firstSse = FakeEventSource.latest()!;

    rerender({ id: 'sess-b' });

    await waitFor(() => {
      expect(result.current.session?.id).toBe('sess-b');
    });
    // The previous EventSource was closed on session change.
    expect(firstSse.closed).toBe(true);
    // And a fresh one was created for sess-b.
    expect(FakeEventSource.instances.length).toBe(2);
    expect(FakeEventSource.latest()!.url).toContain('sess-b');
  });
});

describe('useSession — loadMore', () => {
  function msg(id: string, sessionId: string) {
    return { id, sessionId, timeCreated: 1_000, data: { role: 'user' as const } };
  }

  function notice(id: string, sessionId: string) {
    return { id, sessionId, timeCreated: 1_000, data: { role: 'notice' as const } };
  }

  it('prepends an older page to the current session', async () => {
    const head = makeDetail({ messages: [msg('m2', SID)], totalMessages: 2 });
    const older = makeDetail({ messages: [msg('m1', SID)], totalMessages: 2 });
    const fetchSession = vi.fn()
      .mockResolvedValueOnce(head)
      .mockResolvedValueOnce(older);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(result.current.messages).toHaveLength(1));

    await act(async () => { await result.current.loadMore(); });

    expect(result.current.messages.map((m) => m.id)).toEqual(['m1', 'm2']);
  });

  it('keeps the conversation and stream usable after a failed history page', async () => {
    const failure = new Error('history temporarily unavailable');
    const log = vi.spyOn(remoteLog, 'error').mockImplementation(() => {});
    const fetchSession = vi.fn()
      .mockResolvedValueOnce(makeDetail({ messages: [msg('m2', SID)], totalMessages: 2 }))
      .mockRejectedValueOnce(failure)
      .mockResolvedValueOnce(makeDetail({ messages: [msg('m1', SID)], totalMessages: 2 }));
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(result.current.messages).toHaveLength(1));
    const stream = FakeEventSource.latest();

    await act(async () => { await result.current.loadMore(); });
    expect(result.current.messages.map((message) => message.id)).toEqual(['m2']);
    expect(result.current.loadingMore).toBe(false);
    expect(FakeEventSource.latest()).toBe(stream);
    expect(stream?.closed).toBe(false);
    expect(log).toHaveBeenCalledWith('loadMore failed', failure);

    await act(async () => { await result.current.loadMore(); });
    expect(result.current.messages.map((message) => message.id)).toEqual(['m1', 'm2']);
  });

  it('excludes synthetic notices from the pagination offset', async () => {
    const head = makeDetail({ messages: [msg('m2', SID), notice('n1', SID)], totalMessages: 2 });
    const fetchSession = vi.fn()
      .mockResolvedValueOnce(head)
      .mockResolvedValueOnce(makeDetail({ messages: [msg('m1', SID)], totalMessages: 2 }));
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(result.current.messages).toHaveLength(2));

    await act(async () => { await result.current.loadMore(); });

    expect(fetchSession).toHaveBeenLastCalledWith(SID, 30, 1, expect.any(AbortSignal), undefined, true);
  });

  // A pagination response that lands after the user navigated away
  // used to be dispatched against the captured (old) view, splicing
  // session A's history into session B.
  it('cannot modify the new session when it resolves after a session change', async () => {
    const detailA = makeDetail({
      session: { ...makeDetail().session, id: 'sess-a' },
      messages: [msg('a2', 'sess-a')],
      totalMessages: 2,
    });
    const detailB = makeDetail({
      session: { ...makeDetail().session, id: 'sess-b' },
      messages: [msg('b1', 'sess-b')],
      totalMessages: 1,
    });
    const olderA = makeDetail({
      session: { ...makeDetail().session, id: 'sess-a' },
      messages: [msg('a1', 'sess-a')],
      totalMessages: 2,
    });

    let releaseOlderA: () => void = () => {};
    const olderAGate = new Promise<void>((resolve) => { releaseOlderA = resolve; });

    const fetchSession = vi.fn((id: string, _limit: number, offset: number) => {
      if (id === 'sess-b') return Promise.resolve(detailB);
      if (offset > 0) return olderAGate.then(() => olderA);
      return Promise.resolve(detailA);
    });

    const { result, rerender } = renderHook(({ id }) => useSession(id, { fetchSession }), {
      initialProps: { id: 'sess-a' as string | undefined },
    });
    await waitFor(() => expect(result.current.session?.id).toBe('sess-a'));

    // Start pagination for A, then navigate to B before it resolves.
    const pending = result.current.loadMore();
    rerender({ id: 'sess-b' });
    await waitFor(() => expect(result.current.session?.id).toBe('sess-b'));

    await act(async () => {
      releaseOlderA();
      await pending;
    });

    expect(result.current.sessionId).toBe('sess-b');
    expect(result.current.session?.id).toBe('sess-b');
    expect(result.current.messages.map((m) => m.id)).toEqual(['b1']);
  });

  // The cleanup aborts the in-flight page, but `fetchSession` is an
  // injected seam: an implementation that ignores the signal and never
  // settles never reaches the `finally`. Leaving the in-flight ref set
  // then disables pagination for the rest of the page's life and pins
  // the spinner on.
  it('resets the in-flight state when the session changes mid-page', async () => {
    const detailA = makeDetail({
      session: { ...makeDetail().session, id: 'sess-a' },
      messages: [msg('a2', 'sess-a')],
      totalMessages: 2,
    });
    const detailB = makeDetail({
      session: { ...makeDetail().session, id: 'sess-b' },
      messages: [msg('b2', 'sess-b')],
      totalMessages: 2,
    });
    const olderB = makeDetail({
      session: { ...makeDetail().session, id: 'sess-b' },
      messages: [msg('b1', 'sess-b')],
      totalMessages: 2,
    });

    const fetchSession = vi.fn((id: string, _limit: number, offset: number) => {
      if (offset === 0) return Promise.resolve(id === 'sess-b' ? detailB : detailA);
      // Session A's page never settles and ignores the abort signal.
      if (id === 'sess-a') return new Promise<never>(() => {});
      return Promise.resolve(olderB);
    });

    const { result, rerender } = renderHook(({ id }) => useSession(id, { fetchSession }), {
      initialProps: { id: 'sess-a' as string | undefined },
    });
    await waitFor(() => expect(result.current.session?.id).toBe('sess-a'));

    act(() => { void result.current.loadMore(); });
    await waitFor(() => expect(result.current.loadingMore).toBe(true));

    rerender({ id: 'sess-b' });
    await waitFor(() => expect(result.current.session?.id).toBe('sess-b'));
    expect(result.current.loadingMore).toBe(false);

    // ...and pagination still works on the new session.
    await act(async () => { await result.current.loadMore(); });
    expect(result.current.messages.map((m) => m.id)).toEqual(['b1', 'b2']);
  });

  it('ignores a second call while one is already in flight', async () => {
    const head = makeDetail({ messages: [msg('m2', SID)], totalMessages: 3 });
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const fetchSession = vi.fn((_id: string, _limit: number, offset: number) => {
      if (offset > 0) return gate.then(() => makeDetail({ messages: [msg('m1', SID)], totalMessages: 3 }));
      return Promise.resolve(head);
    });
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(result.current.messages).toHaveLength(1));

    // Both calls issued in the same tick — `loadingMore` state has not
    // re-rendered yet, so only an in-flight ref can block the second.
    const first = result.current.loadMore();
    const second = result.current.loadMore();
    await act(async () => {
      release();
      await Promise.all([first, second]);
    });

    // 1 head fetch + exactly 1 pagination fetch.
    expect(fetchSession).toHaveBeenCalledTimes(2);
  });
});

describe('useSession — unmount', () => {
  it('closes the EventSource on unmount', async () => {
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const { unmount } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => {
      expect(FakeEventSource.latest()).toBeDefined();
    });
    const sse = FakeEventSource.latest()!;
    unmount();
    expect(sse.closed).toBe(true);
  });
});

describe('useSession — keepalive watchdog', () => {
  it('replaces a stream that stops receiving keepalives and reconciles', async () => {
    vi.useFakeTimers();
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    const first = FakeEventSource.latest()!;
    act(() => { first.open(); first.emitNamed('ocman.keepalive', {}); });

    // Keepalives hold the stream.
    act(() => { vi.advanceTimersByTime(KEEPALIVE_TIMEOUT_MS - 1); first.emitNamed('ocman.keepalive', {}); });
    act(() => { vi.advanceTimersByTime(KEEPALIVE_TIMEOUT_MS - 1); });
    expect(FakeEventSource.instances).toHaveLength(1);

    // Half-open: no error, just silence.
    act(() => { vi.advanceTimersByTime(1); });
    expect(first.closed).toBe(true);
    const second = FakeEventSource.latest()!;
    expect(second).not.toBe(first);
    const fetches = fetchSession.mock.calls.length;
    await act(async () => { second.open(); second.emitOce(hello(false)); });
    expect(fetchSession.mock.calls.length).toBe(fetches + 1);
  });
});

describe('useSession — keepalive watchdog before first open', () => {
  // The snapshot loaded but the first stream hung connecting; events from
  // that window are not replayed, so the replacement must reconcile.
  it('reconciles when replacing a stream that never opened', async () => {
    vi.useFakeTimers();
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    const first = FakeEventSource.latest()!;

    act(() => { vi.advanceTimersByTime(KEEPALIVE_TIMEOUT_MS); });
    expect(first.closed).toBe(true);
    const fetches = fetchSession.mock.calls.length;
    await act(async () => { FakeEventSource.latest()!.open(); FakeEventSource.latest()!.emitOce(hello(false)); });
    expect(fetchSession.mock.calls.length).toBe(fetches + 1);
  });
});

describe('useSession — retryNow', () => {
  it('cancels the backoff timer and reconnects immediately', async () => {
    vi.useFakeTimers();
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() =>
      useSession(SID, { fetchSession, reconnectDelay: () => 30_000 }),
    );

    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    const first = FakeEventSource.latest()!;
    act(() => first.open());

    // Trigger an error — this starts the 30 s backoff.
    act(() => first.error());
    expect(result.current.sseReconnecting).toBe(true);
    expect(result.current.sseNextRetryAt).not.toBeNull();

    // Call retryNow — should reconnect without waiting 30 s.
    await act(async () => {
      result.current.retryNow();
      // No timer advancement needed — retryNow calls connect() directly.
      await Promise.resolve();
    });

    // A new EventSource should exist.
    expect(FakeEventSource.instances.length).toBe(2);
    // sseNextRetryAt should be cleared synchronously.
    expect(result.current.sseNextRetryAt).toBeNull();
  });

  it('opens the new connection to the correct URL after retryNow', async () => {
    vi.useFakeTimers();
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() =>
      useSession(SID, { fetchSession, reconnectDelay: () => 30_000 }),
    );

    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    act(() => FakeEventSource.latest()!.open());
    act(() => FakeEventSource.latest()!.error());

    await act(async () => {
      result.current.retryNow();
      await Promise.resolve();
    });

    const second = FakeEventSource.latest()!;
    expect(second.url).toContain(`/api/session/${SID}/stream`);
  });

  it('transitions back to live after retryNow opens successfully', async () => {
    vi.useFakeTimers();
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() =>
      useSession(SID, { fetchSession, reconnectDelay: () => 30_000 }),
    );

    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    act(() => FakeEventSource.latest()!.open());
    act(() => FakeEventSource.latest()!.error());
    expect(result.current.status).toBe('reconnecting');

    await act(async () => {
      result.current.retryNow();
      await Promise.resolve();
    });

    // Open the new stream.
    act(() => FakeEventSource.latest()!.open());
    expect(result.current.status).toBe('live');
    expect(result.current.sseReconnecting).toBe(false);
  });

  it('is a no-op after unmount', async () => {
    vi.useFakeTimers();
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const { result, unmount } = renderHook(() =>
      useSession(SID, { fetchSession, reconnectDelay: () => 30_000 }),
    );

    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    act(() => FakeEventSource.latest()!.open());
    act(() => FakeEventSource.latest()!.error());

    unmount();

    // retryNow should not create a new EventSource after unmount.
    act(() => result.current.retryNow());
    expect(FakeEventSource.instances.length).toBe(1);
  });
});

describe('useSession — subagent event routing', () => {
  async function connected(detail = makeDetail()) {
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const hook = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(hook.result.current.session?.id).toBe(SID));
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());
    return { ...hook, sse };
  }

  it('dispatches envelopes for the page session', async () => {
    const { result, sse } = await connected();
    act(() => sse.emitOce({ type: 'message.upserted', message: { id: 'm-parent', sessionId: SID, timeCreated: 100, data: { role: 'assistant' } } }));
    await waitFor(() => expect(result.current.messages.find((m) => m.id === 'm-parent')).toBeDefined());
  });

  it('drops envelopes addressed to a different (subagent) session', async () => {
    const { result, sse } = await connected();
    act(() => {
      sse.emitOce({ type: 'message.upserted', message: { id: 'm-subagent', sessionId: 'subagent-sess-99', timeCreated: 100, data: { role: 'assistant' } } }, { sessionId: 'subagent-sess-99' });
      sse.emitOce(textDelta('p-sub', 0, 'streaming', 'm-sub'), { sessionId: 'subagent-sess-99' });
    });
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 60)); });
    expect(result.current.messages.find((m) => m.id === 'm-subagent')).toBeUndefined();
    expect(result.current.parts.find((p) => p.id === 'p-sub')).toBeUndefined();
  });

  it('surfaces a descendant prompt the owner routed to this stream and tracks child status', async () => {
    const parent = makeDetail().session;
    const { result, sse } = await connected(makeDetail({ sessionTree: [parent, { ...parent, id: 'child', parentId: SID, status: 'busy' }] }));
    act(() => {
      sse.emitOce({ type: 'prompt.asked', prompt: { id: 'perm-child', kind: 'permission', sessionId: 'child', askedAt: 5, permission: { permission: 'bash', patterns: ['ls'] } } });
      sse.emitOce({ type: 'child.status', childId: 'child', status: 'waiting' });
    });
    await waitFor(() => expect(result.current.pendingPermission).toMatchObject({ permissionId: 'perm-child', sessionId: 'child', askedAt: 5 }));
    expect(result.current.sessionTree.find((s) => s.id === 'child')?.status).toBe('waiting');
  });
});

// #460: the cache mirror must not write on every streaming token delta —
// each write clones the whole sessionCache Map and notifies every store
// subscriber. It should batch (debounce) and flush on unmount so a
// revisit is still warm.
describe('useSession — cache mirror write amplification (#460)', () => {
  it('writes O(1) per burst of deltas, with an unmount flush', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result, unmount } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(result.current.session?.id).toBe(SID));

    // Seed the cache entry (updateCachedSession no-ops otherwise) and
    // start counting writes.
    act(() => useApiStore.getState().setCachedSession(SID, detail));
    const original = useApiStore.getState().updateCachedSession;
    const spy = vi.fn(original);
    useApiStore.setState({ updateCachedSession: spy });

    const sse = FakeEventSource.latest()!;
    act(() => sse.open());
    act(() => {
      for (let i = 0; i < 50; i++) sse.emitOce(textDelta('p-burst', i, 'x', 'm-burst'));
    });

    expect(spy.mock.calls.length).toBeLessThanOrEqual(1);

    // Unmount flushes the latest view so a revisit is warm.
    unmount();
    const cached = useApiStore.getState().getCachedSession(SID);
    const part = cached?.parts.find((p) => p.id === 'p-burst');
    const data = typeof part?.data === 'string' ? JSON.parse(part.data) : part?.data;
    expect(data?.text).toBe('x'.repeat(50));
  });
});
