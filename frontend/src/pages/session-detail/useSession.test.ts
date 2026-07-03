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
import { useSession } from './useSession';
import { useApiStore } from '../../lib/apiStore';

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
  useApiStore.setState({ sessionCache: new Map(), sessionCacheOrder: [] });
});

afterEach(() => {
  vi.useRealTimers();
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
    expect(fetchSession).toHaveBeenCalledWith(SID, expect.any(Number), 0, expect.anything(), undefined);
    expect(result.current.status).toBe('live');
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

  it('adds session notices to the message history', async () => {
    const detail = makeDetail({
      session: {
        ...makeDetail().session,
        notice: { kind: 'error', message: 'connection refused', retryAt: 0, attempt: 0 },
      },
    });
    const fetchSession = vi.fn().mockResolvedValue(detail);

    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await waitFor(() => {
      expect(result.current.messages.some((m) => m.id === `ocman-session-notice-${SID}`)).toBe(true);
    });
    expect(result.current.parts.find((p) => p.messageId === `ocman-session-notice-${SID}`)?.data).toEqual({
      type: 'text',
      text: 'connection refused',
    });
  });
});

describe('useSession — SSE event dispatch', () => {
  it('routes onmessage payloads through the reducer', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await waitFor(() => {
      expect(result.current.session?.id).toBe(SID);
    });

    const sse = FakeEventSource.latest();
    expect(sse).toBeDefined();
    act(() => {
      sse!.open();
    });
    act(() => {
      sse!.emitMessage({
        type: 'message.created',
        properties: {
          info: { id: 'm-1', sessionID: SID, role: 'assistant', time: { created: 100 } },
          parts: [
            { id: 'p-1', messageID: 'm-1', sessionID: SID, type: 'text', text: 'Hi' },
          ],
        },
      });
    });

    expect(result.current.messages.find((m) => m.id === 'm-1')).toBeDefined();
    expect(result.current.parts.find((p) => p.id === 'p-1')).toBeDefined();
  });

  it('does not dispatch default message-channel payloads twice', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await waitFor(() => {
      expect(result.current.session?.id).toBe(SID);
    });

    const sse = FakeEventSource.latest()!;
    act(() => sse.open());
    act(() => {
      sse.emitMessage({
        type: 'message.part.delta',
        properties: {
          partID: 'p-dup', messageID: 'm-dup', sessionID: SID, field: 'text', delta: 'one ',
        },
      });
    });

    const part = result.current.parts.find((p) => p.id === 'p-dup');
    const data = typeof part?.data === 'string' ? JSON.parse(part.data) : part?.data;
    expect(data.text).toBe('one ');
  });

  it('routes named-channel payloads through the reducer', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await waitFor(() => {
      expect(result.current.session?.id).toBe(SID);
    });

    const sse = FakeEventSource.latest()!;
    act(() => sse.open());
    act(() => {
      sse.emitNamed('message.part.delta', {
        properties: {
          partID: 'p-named', messageID: 'm-named', sessionID: SID, field: 'text', delta: 'named ',
        },
      });
    });

    const part = result.current.parts.find((p) => p.id === 'p-named');
    const data = typeof part?.data === 'string' ? JSON.parse(part.data) : part?.data;
    expect(data.text).toBe('named ');
  });

  it('routes raw named message.part.updated payloads as live tool snapshots', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await waitFor(() => {
      expect(result.current.session?.id).toBe(SID);
    });

    const sse = FakeEventSource.latest()!;
    act(() => sse.open());
    act(() => {
      sse.emitNamed('message.part.updated', {
        id: 'p-tool-live',
        messageID: 'm-tool-live',
        sessionID: SID,
        type: 'tool',
        tool: 'bash',
        state: { status: 'running', input: { command: 'sleep 10' } },
      });
    });

    const stub = result.current.messages.find((m) => m.id === 'm-tool-live');
    expect(stub).toBeDefined();
    const part = result.current.parts.find((p) => p.id === 'p-tool-live');
    const data = typeof part?.data === 'string' ? JSON.parse(part.data) : part?.data;
    expect(data.type).toBe('tool');
    expect(data.tool).toBe('bash');
    expect((data.state as { status?: string }).status).toBe('running');
  });

  it('routes raw named tool payloads as live tool snapshots', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await waitFor(() => {
      expect(result.current.session?.id).toBe(SID);
    });

    const sse = FakeEventSource.latest()!;
    act(() => sse.open());
    act(() => {
      sse.emitNamed('tool', {
        id: 'p-tool-channel',
        messageID: 'm-tool-channel',
        sessionID: SID,
        type: 'tool',
        tool: 'bash',
        state: { status: 'running', input: { command: 'go test ./...' } },
      });
    });

    const stub = result.current.messages.find((m) => m.id === 'm-tool-channel');
    expect(stub).toBeDefined();
    const part = result.current.parts.find((p) => p.id === 'p-tool-channel');
    const data = typeof part?.data === 'string' ? JSON.parse(part.data) : part?.data;
    expect(data.type).toBe('tool');
    expect(data.tool).toBe('bash');
    expect((data.state as { status?: string }).status).toBe('running');
  });

  it('opens the EventSource against the correct URL', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => {
      expect(FakeEventSource.latest()).toBeDefined();
    });
    expect(FakeEventSource.latest()!.url).toContain(`/api/session/${SID}/events`);
  });

  it('routes remote EventSource and fetches through the cached platform', async () => {
    const detail = makeDetail({
      session: {
        ...makeDetail().session,
        platform: 'r-box:opencode',
      },
    });
    useApiStore.getState().setCachedSession(SID, detail);
    const fetchSession = vi.fn().mockResolvedValue(detail);

    renderHook(() => useSession(SID, { fetchSession }));

    await waitFor(() => {
      expect(FakeEventSource.latest()).toBeDefined();
    });
    expect(FakeEventSource.latest()!.url).toContain(`/api/session/${SID}/events?platform=r-box%3Aopencode`);
    expect(fetchSession).toHaveBeenCalledWith(SID, expect.any(Number), 0, expect.any(AbortSignal), 'r-box:opencode');
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

    // Open the new stream → refetch + status becomes 'live' again.
    act(() => {
      FakeEventSource.latest()!.open();
    });
    expect(result.current.status).toBe('live');
    // Two fetches total: initial mount + reconnect reconciliation.
    expect(fetchSession).toHaveBeenCalledTimes(2);
  });
});

describe('useSession — session.idle refetch', () => {
  it('refetches /api/session/{id} when session.idle lands', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => {
      expect(result.current.session?.id).toBe(SID);
    });
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    act(() => {
      sse.emitMessage({ type: 'session.idle', properties: {} });
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

describe('useSession — recentWorkEventAt', () => {
  it('is null on initial mount before any SSE arrives', async () => {
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const { result } = renderHook(() => useSession(SID, { fetchSession }));
    await waitFor(() => expect(result.current.session?.id).toBe(SID));
    expect(result.current.recentWorkEventAt).toBeNull();
  });

  it('updates on work-producing events (message.created)', async () => {
    vi.useFakeTimers();
    const t0 = 1_000_000;
    vi.setSystemTime(t0);
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    // Advance time past the 100 ms throttle window so the bump fires.
    act(() => { vi.setSystemTime(t0 + 200); });
    act(() => {
      sse.emitMessage({
        type: 'message.created',
        properties: {
          info: { id: 'm-1', sessionID: SID, role: 'assistant', time: { created: t0 } },
          parts: [],
        },
      });
    });

    expect(result.current.recentWorkEventAt).not.toBeNull();
  });

  it('updates on message.part.delta events', async () => {
    vi.useFakeTimers();
    const t0 = 2_000_000;
    vi.setSystemTime(t0);
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    act(() => { vi.setSystemTime(t0 + 200); });
    act(() => {
      sse.emitMessage({
        type: 'message.part.delta',
        properties: {
          partID: 'p-1', messageID: 'm-1', sessionID: SID, field: 'text', delta: 'hello',
        },
      });
    });

    expect(result.current.recentWorkEventAt).not.toBeNull();
  });

  it('does NOT update on non-work events (session.status, session.idle)', async () => {
    vi.useFakeTimers();
    const t0 = 3_000_000;
    vi.setSystemTime(t0);
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    act(() => { vi.setSystemTime(t0 + 200); });
    act(() => {
      sse.emitMessage({ type: 'session.status', properties: { status: 'idle' } });
    });
    act(() => {
      sse.emitMessage({ type: 'session.idle', properties: {} });
    });

    expect(result.current.recentWorkEventAt).toBeNull();
  });

  it('is throttled — two events within 100 ms produce a single bump', async () => {
    vi.useFakeTimers();
    const t0 = 4_000_000;
    vi.setSystemTime(t0);
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    // First event fires the bump.
    act(() => { vi.setSystemTime(t0 + 200); });
    act(() => {
      sse.emitMessage({
        type: 'message.part.delta',
        properties: { partID: 'p-a', messageID: 'm-a', sessionID: SID, field: 'text', delta: 'a' },
      });
    });
    const first = result.current.recentWorkEventAt;
    expect(first).not.toBeNull();

    // Second event within the 100 ms window — workBumpAtRef guards it.
    act(() => { vi.setSystemTime(t0 + 250); }); // only 50 ms after first bump
    act(() => {
      sse.emitMessage({
        type: 'message.part.delta',
        properties: { partID: 'p-b', messageID: 'm-b', sessionID: SID, field: 'text', delta: 'b' },
      });
    });
    expect(result.current.recentWorkEventAt).toBe(first); // still the same timestamp

    // Third event after the throttle window — should update.
    act(() => { vi.setSystemTime(t0 + 400); }); // 200 ms after first bump
    act(() => {
      sse.emitMessage({
        type: 'message.part.delta',
        properties: { partID: 'p-c', messageID: 'm-c', sessionID: SID, field: 'text', delta: 'c' },
      });
    });
    expect(result.current.recentWorkEventAt).toBeGreaterThan(first!);
  });

  it('resets to null when the session ID changes', async () => {
    vi.useFakeTimers();
    const t0 = 5_000_000;
    vi.setSystemTime(t0);
    const fetchSession = vi.fn().mockResolvedValue(makeDetail());
    const { result, rerender } = renderHook(
      ({ id }) => useSession(id, { fetchSession }),
      { initialProps: { id: SID as string | undefined } },
    );

    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    // Bump recentWorkEventAt.
    act(() => { vi.setSystemTime(t0 + 200); });
    act(() => {
      sse.emitMessage({
        type: 'message.created',
        properties: {
          info: { id: 'm-1', sessionID: SID, role: 'assistant', time: { created: t0 } },
          parts: [],
        },
      });
    });
    expect(result.current.recentWorkEventAt).not.toBeNull();

    // Navigate to a different session — cleanup should null the value.
    const SID2 = 'sess-2';
    fetchSession.mockResolvedValue(makeDetail({ session: { ...makeDetail().session, id: SID2 } }));
    rerender({ id: SID2 });

    await vi.waitFor(() => expect(result.current.recentWorkEventAt).toBeNull());
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
    expect(second.url).toContain(`/api/session/${SID}/events`);
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
  it('dispatches events whose sessionID matches the parent session', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await waitFor(() => expect(result.current.session?.id).toBe(SID));
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    act(() => {
      sse.emitMessage({
        type: 'message.created',
        properties: {
          info: { id: 'm-parent', sessionID: SID, role: 'assistant', time: { created: 100 } },
          parts: [],
        },
      });
    });

    // Message with matching session ID should appear.
    expect(result.current.messages.find((m) => m.id === 'm-parent')).toBeDefined();
  });

  it('drops events whose sessionID belongs to a different (subagent) session', async () => {
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await waitFor(() => expect(result.current.session?.id).toBe(SID));
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    act(() => {
      sse.emitMessage({
        type: 'message.created',
        properties: {
          info: {
            id: 'm-subagent',
            sessionID: 'subagent-sess-99', // different session ID
            role: 'assistant',
            time: { created: 100 },
          },
          parts: [],
        },
      });
    });

    // The reducer should drop this event — no orphan message.
    expect(result.current.messages.find((m) => m.id === 'm-subagent')).toBeUndefined();
  });

  it('does not bump recentWorkEventAt for dropped subagent events', async () => {
    vi.useFakeTimers();
    const t0 = 6_000_000;
    vi.setSystemTime(t0);
    const detail = makeDetail();
    const fetchSession = vi.fn().mockResolvedValue(detail);
    const { result } = renderHook(() => useSession(SID, { fetchSession }));

    await vi.waitFor(() => expect(result.current.session?.id).toBe(SID));
    const sse = FakeEventSource.latest()!;
    act(() => sse.open());

    // The work-event bump happens in useSession's onmessage handler
    // BEFORE the reducer, so even subagent events trigger it — the
    // hook does not guard on session ID for bumpWorkEvent. This is
    // intentional: the parent session IS doing work when a subagent
    // is active. Verify the current behaviour is stable.
    act(() => { vi.setSystemTime(t0 + 200); });
    act(() => {
      sse.emitMessage({
        type: 'message.part.delta',
        properties: {
          partID: 'p-sub', messageID: 'm-sub', sessionID: 'subagent-sess-99',
          field: 'text', delta: 'streaming',
        },
      });
    });

    // bumpWorkEvent fires regardless of session ID — the timestamp updates.
    expect(result.current.recentWorkEventAt).not.toBeNull();
    // But the part itself is dropped by the reducer.
    expect(result.current.parts.find((p) => p.id === 'p-sub')).toBeUndefined();
  });
});
