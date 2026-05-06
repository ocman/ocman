// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useRef } from 'react';
import { useSessionSSE } from './useSessionSSE';
import { SSE_BACKOFF_BASE_MS } from './sseBackoff';

// --- Fake EventSource -------------------------------------------------
//
// jsdom doesn't ship a usable EventSource. We install a controllable
// stub so tests can drive `onopen`/`onerror`/`onmessage` and assert the
// hook's reconnect schedule. Each instance is captured in
// `EventSourceStub.instances` so tests can grab the latest connection.

class EventSourceStub {
  static instances: EventSourceStub[] = [];
  static OPEN = 1;
  static CONNECTING = 0;
  static CLOSED = 2;

  url: string;
  readyState = EventSourceStub.CONNECTING;
  onopen: ((evt: Event) => void) | null = null;
  onerror: ((evt: Event) => void) | null = null;
  onmessage: ((evt: MessageEvent) => void) | null = null;
  // We don't care about named events for these tests; record them so
  // we don't blow up when the hook installs listeners.
  listeners: Record<string, Array<(evt: Event) => void>> = {};

  constructor(url: string) {
    this.url = url;
    EventSourceStub.instances.push(this);
  }

  addEventListener(name: string, cb: (evt: Event) => void) {
    (this.listeners[name] ||= []).push(cb);
  }

  close() {
    this.readyState = EventSourceStub.CLOSED;
  }

  // Helpers for tests.
  triggerOpen() {
    this.readyState = EventSourceStub.OPEN;
    this.onopen?.(new Event('open'));
  }
  triggerError() {
    this.onerror?.(new Event('error'));
  }
  triggerMessage(data: Record<string, unknown>) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(data) }));
  }
}

// --- Hook harness ------------------------------------------------------
//
// useSessionSSE takes a swarm of refs/setters from its parent page.
// We build minimal stubs and ignore their values — the tests only
// observe the hook's own `sseActive` / reconnect state.

function makeOptions(sessionId: string | undefined) {
  const noop = () => {};
  // The hook re-runs its effect whenever `load` changes identity,
  // which would tear down and recreate the EventSource on every
  // re-render. We hoist `load` out of the per-render builder so its
  // reference stays stable across renders — mirroring how the real
  // page passes a `useCallback`-stable function in.
  const stableLoad = vi.fn(async () => {});
  // useRef can only be called inside a component. We expose a tiny
  // wrapper that creates the refs at hook-call time so renderHook can
  // re-create them on each render.
  return () => {
    const abortRef = useRef<AbortController | null>(null);
    const loadErrorRef = useRef<string | null>(null);
    const debugModeRef = useRef<boolean>(false);
    const subagentSessionIdsRef = useRef<Set<string>>(new Set());
    const activeSessionIdRef = useRef<string | undefined>(sessionId);
    return {
      sessionId,
      directory: '/tmp/x',
      load: stableLoad,
      abortSignalRef: abortRef,
      loadErrorRef,
      debugModeRef,
      subagentSessionIdsRef,
      activeSessionIdRef,
      setMessages: noop,
      setParts: noop,
      setSession: noop,
      setPortAvailable: noop,
      setPendingPermission: noop,
      setPermissionError: noop,
      setPendingQuestion: noop,
      setSubagentTokens: noop,
      setChangesDirtyTick: noop,
    };
  };
}

beforeEach(() => {
  EventSourceStub.instances = [];
  vi.stubGlobal('EventSource', EventSourceStub as unknown as typeof EventSource);
  vi.useFakeTimers({ shouldAdvanceTime: false });
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('useSessionSSE reconnect behaviour', () => {
  it('exposes sseReconnecting=false and attempt=0 before any disconnect', () => {
    const buildOpts = makeOptions('s1');
    const { result } = renderHook(() => useSessionSSE(buildOpts()));

    expect(result.current.sseActive).toBe(false);
    expect(result.current.sseReconnecting).toBe(false);
    expect(result.current.sseReconnectAttempt).toBe(0);
    expect(result.current.sseNextRetryAt).toBeNull();
  });

  it('flips sseActive to true on EventSource open', () => {
    const buildOpts = makeOptions('s1');
    const { result } = renderHook(() => useSessionSSE(buildOpts()));

    act(() => {
      EventSourceStub.instances[0].triggerOpen();
    });
    expect(result.current.sseActive).toBe(true);
    expect(result.current.sseReconnecting).toBe(false);
    expect(result.current.sseReconnectAttempt).toBe(0);
  });

  it('schedules the first reconnect at the base delay (500 ms)', () => {
    const buildOpts = makeOptions('s1');
    const { result } = renderHook(() => useSessionSSE(buildOpts()));

    act(() => {
      EventSourceStub.instances[0].triggerOpen();
    });
    act(() => {
      EventSourceStub.instances[0].triggerError();
    });

    expect(result.current.sseActive).toBe(false);
    expect(result.current.sseReconnecting).toBe(true);
    expect(result.current.sseReconnectAttempt).toBe(1);
    expect(result.current.sseNextRetryAt).not.toBeNull();
    // Base delay, no jitter.
    expect(EventSourceStub.instances).toHaveLength(1);

    act(() => {
      vi.advanceTimersByTime(SSE_BACKOFF_BASE_MS);
    });
    expect(EventSourceStub.instances).toHaveLength(2);
  });

  it('uses an increasing backoff for repeated failures', () => {
    // Force jitter to the deterministic midpoint so we can assert
    // exact delays from the equal-jitter formula in sseBackoff.
    const randomSpy = vi.spyOn(Math, 'random').mockReturnValue(0.5);
    const buildOpts = makeOptions('s1');
    const { result } = renderHook(() => useSessionSSE(buildOpts()));

    // First failure: 500 ms, no jitter.
    act(() => {
      EventSourceStub.instances[0].triggerOpen();
    });
    act(() => {
      EventSourceStub.instances[0].triggerError();
    });
    expect(result.current.sseReconnectAttempt).toBe(1);
    act(() => {
      vi.advanceTimersByTime(500);
    });
    expect(EventSourceStub.instances).toHaveLength(2);

    // Second failure (no successful open in between, so attempt
    // counter keeps climbing): equal-jitter target=1000, midpoint=750.
    act(() => {
      EventSourceStub.instances[1].triggerError();
    });
    expect(result.current.sseReconnectAttempt).toBe(2);
    act(() => {
      vi.advanceTimersByTime(749);
    });
    expect(EventSourceStub.instances).toHaveLength(2);
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(EventSourceStub.instances).toHaveLength(3);

    // Third failure: target=2000, midpoint=1500.
    act(() => {
      EventSourceStub.instances[2].triggerError();
    });
    expect(result.current.sseReconnectAttempt).toBe(3);
    act(() => {
      vi.advanceTimersByTime(1500);
    });
    expect(EventSourceStub.instances).toHaveLength(4);

    randomSpy.mockRestore();
  });

  it('resets the reconnect counter after a successful reopen', () => {
    const buildOpts = makeOptions('s1');
    const { result } = renderHook(() => useSessionSSE(buildOpts()));

    act(() => {
      EventSourceStub.instances[0].triggerOpen();
    });
    act(() => {
      EventSourceStub.instances[0].triggerError();
    });
    expect(result.current.sseReconnectAttempt).toBe(1);

    // Fast-forward past the base delay; the second connection opens
    // successfully.
    act(() => {
      vi.advanceTimersByTime(SSE_BACKOFF_BASE_MS);
    });
    act(() => {
      EventSourceStub.instances[1].triggerOpen();
    });
    expect(result.current.sseReconnectAttempt).toBe(0);
    expect(result.current.sseReconnecting).toBe(false);
    expect(result.current.sseActive).toBe(true);
  });

  it('retryNow cancels the pending timer and reconnects immediately', () => {
    const buildOpts = makeOptions('s1');
    const { result } = renderHook(() => useSessionSSE(buildOpts()));

    act(() => {
      EventSourceStub.instances[0].triggerOpen();
    });
    act(() => {
      EventSourceStub.instances[0].triggerError();
    });
    expect(EventSourceStub.instances).toHaveLength(1);

    act(() => {
      result.current.retryNow();
    });
    expect(EventSourceStub.instances).toHaveLength(2);

    // The original timer must not also fire after retryNow — otherwise
    // we'd double-connect.
    act(() => {
      vi.advanceTimersByTime(SSE_BACKOFF_BASE_MS * 2);
    });
    expect(EventSourceStub.instances).toHaveLength(2);
  });

  it('caps the backoff delay at one minute', () => {
    // random=1 puts every attempt at the top of the jitter window.
    const randomSpy = vi.spyOn(Math, 'random').mockReturnValue(1);
    const buildOpts = makeOptions('s1');
    const { result } = renderHook(() => useSessionSSE(buildOpts()));

    // Drive 10 consecutive failures with no successful opens.
    act(() => {
      EventSourceStub.instances[0].triggerOpen();
    });
    for (let i = 0; i < 10; i++) {
      act(() => {
        EventSourceStub.instances[i].triggerError();
      });
      // Skip past whatever delay was scheduled (cap is 60s).
      act(() => {
        vi.advanceTimersByTime(60_000);
      });
    }
    // After many failures the next-retry stamp should never project
    // more than a minute into the future.
    expect(result.current.sseReconnectAttempt).toBeGreaterThanOrEqual(10);
    if (result.current.sseNextRetryAt !== null) {
      expect(result.current.sseNextRetryAt - Date.now()).toBeLessThanOrEqual(60_000);
    }
    randomSpy.mockRestore();
  });
});

describe('useSessionSSE activity tracking', () => {
  it('records activity for a tool part update', () => {
    const stableLoad = vi.fn(async () => {});
    const setSession = vi.fn();
    const noop = vi.fn();
    const { result } = renderHook(() => {
      const abortRef = useRef<AbortController | null>(null);
      const loadErrorRef = useRef<string | null>(null);
      const debugModeRef = useRef(false);
      const subagentSessionIdsRef = useRef<Set<string>>(new Set());
      const activeSessionIdRef = useRef<string | undefined>('s1');
      return useSessionSSE({
        sessionId: 's1',
        directory: '/tmp/x',
        load: stableLoad,
        abortSignalRef: abortRef,
        loadErrorRef,
        debugModeRef,
        subagentSessionIdsRef,
        activeSessionIdRef,
        setMessages: noop,
        setParts: noop,
        setSession,
        setPortAvailable: noop,
        setPendingPermission: noop,
        setPermissionError: noop,
        setPendingQuestion: noop,
        setSubagentTokens: noop,
        setChangesDirtyTick: noop,
      });
    });

    act(() => {
      EventSourceStub.instances[0].triggerMessage({
        type: 'message.part.updated',
        properties: {
          part: {
            id: 'p1',
            messageID: 'm1',
            sessionID: 's1',
            type: 'tool',
            tool: 'bash',
          },
        },
      });
    });

    expect(result.current.recentWorkEventAt).not.toBeNull();
  });

  it('treats subagent message events as parent-session activity', () => {
    const stableLoad = vi.fn(async () => {});
    const setSession = vi.fn();
    const noop = vi.fn();
    const { result } = renderHook(() => {
      const abortRef = useRef<AbortController | null>(null);
      const loadErrorRef = useRef<string | null>(null);
      const debugModeRef = useRef(false);
      const subagentSessionIdsRef = useRef<Set<string>>(new Set(['sub-1']));
      const activeSessionIdRef = useRef<string | undefined>('s1');
      return useSessionSSE({
        sessionId: 's1',
        directory: '/tmp/x',
        load: stableLoad,
        abortSignalRef: abortRef,
        loadErrorRef,
        debugModeRef,
        subagentSessionIdsRef,
        activeSessionIdRef,
        setMessages: noop,
        setParts: noop,
        setSession,
        setPortAvailable: noop,
        setPendingPermission: noop,
        setPermissionError: noop,
        setPendingQuestion: noop,
        setSubagentTokens: noop,
        setChangesDirtyTick: noop,
      });
    });

    act(() => {
      EventSourceStub.instances[0].triggerMessage({
        type: 'message.updated',
        properties: {
          sessionID: 'sub-1',
          info: {
            sessionID: 'sub-1',
            id: 'm-sub-1',
            role: 'assistant',
            tokens: { output: 12 },
            time: { created: Date.now() - 100 },
          },
        },
      });
    });

    expect(result.current.recentWorkEventAt).not.toBeNull();
  });

  it('does not treat prompt bookkeeping alone as work activity', () => {
    const stableLoad = vi.fn(async () => {});
    const noop = vi.fn();
    const { result } = renderHook(() => {
      const abortRef = useRef<AbortController | null>(null);
      const loadErrorRef = useRef<string | null>(null);
      const debugModeRef = useRef(false);
      const subagentSessionIdsRef = useRef<Set<string>>(new Set());
      const activeSessionIdRef = useRef<string | undefined>('s1');
      return useSessionSSE({
        sessionId: 's1',
        directory: '/tmp/x',
        load: stableLoad,
        abortSignalRef: abortRef,
        loadErrorRef,
        debugModeRef,
        subagentSessionIdsRef,
        activeSessionIdRef,
        setMessages: noop,
        setParts: noop,
        setSession: noop,
        setPortAvailable: noop,
        setPendingPermission: noop,
        setPermissionError: noop,
        setPendingQuestion: noop,
        setSubagentTokens: noop,
        setChangesDirtyTick: noop,
      });
    });

    act(() => {
      EventSourceStub.instances[0].triggerMessage({
        type: 'question.asked',
        properties: {
          sessionID: 's1',
          id: 'q1',
          text: 'Continue?',
        },
      });
    });

    expect(result.current.recentWorkEventAt).toBeNull();
  });
});
