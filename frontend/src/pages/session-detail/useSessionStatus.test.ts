// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import type { Message } from '../../lib/api';
import { useSessionStatus } from './useSessionStatus';
import type { SubagentTokenMap } from './useSubagentTracking';

// Builds a minimal assistant Message with the time + token fields the
// TPS computation reads. All other fields are left undefined since
// the hook doesn't touch them.
function assistantMessage(opts: {
  id: string;
  created: number;
  completed?: number;
  output: number;
}): Message {
  return {
    id: opts.id,
    sessionId: 's1',
    timeCreated: opts.created,
    data: {
      role: 'assistant',
      tokens: { input: 0, output: opts.output },
      time: { created: opts.created, completed: opts.completed },
    },
  };
}

function userMessage(id: string, created: number): Message {
  return {
    id,
    sessionId: 's1',
    timeCreated: created,
    data: { role: 'user', time: { created } },
  };
}

interface HookProps {
  lastMsg: Message | null;
  messages: Message[];
  subagentTokens: SubagentTokenMap;
  isRunning: boolean;
  pendingPermission: null;
  pendingQuestion: null;
}

function buildHook(initial: HookProps) {
  const setSubagentTokens = vi.fn();
  const utils = renderHook(
    (props: HookProps) =>
      useSessionStatus({
        lastMsg: props.lastMsg,
        messages: props.messages,
        subagentTokens: props.subagentTokens,
        // Pass a fresh function identity on every render to mirror what
        // useSubagentTracking does today. The hook must not let an
        // unstable setter re-arm its 1 Hz interval, otherwise every
        // parent rerender would resynchronously recompute TPS.
        setSubagentTokens: (next) => setSubagentTokens(next),
        isRunning: props.isRunning,
        pendingPermission: props.pendingPermission,
        pendingQuestion: props.pendingQuestion,
      }),
    { initialProps: initial },
  );
  return { ...utils, setSubagentTokens };
}

describe('useSessionStatus liveTokensPerSecond', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2024-01-01T00:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('updates at most once per second even when messages stream in rapidly', () => {
    const t0 = Date.now();
    const user = userMessage('u1', t0 - 5_000);

    // Initial: assistant just started 200 ms ago, 50 output tokens.
    let messages: Message[] = [
      user,
      assistantMessage({ id: 'a1', created: t0 - 200, output: 50 }),
    ];

    const { result, rerender } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      isRunning: true,
      pendingPermission: null,
      pendingQuestion: null,
    });

    // Synchronous compute on mount — first reading available immediately.
    const firstReading = result.current.liveTokensPerSecond;
    expect(firstReading).not.toBeNull();
    expect(firstReading).toBeGreaterThan(0);

    // Simulate a flurry of SSE deltas: each delta produces a new
    // `messages` array reference and a higher output count, but we
    // never advance time. With the old implementation each rerender
    // would re-run the effect synchronously and push a new TPS value;
    // the new implementation must hold the last value steady until
    // the 1 Hz interval ticks.
    for (let i = 1; i <= 10; i++) {
      messages = [
        user,
        assistantMessage({ id: 'a1', created: t0 - 200, output: 50 + i * 10 }),
      ];
      rerender({
        lastMsg: messages[messages.length - 1],
        messages,
        subagentTokens: new Map(),
        isRunning: true,
        pendingPermission: null,
        pendingQuestion: null,
      });
    }

    // No wall-clock time has elapsed → reading should be unchanged.
    expect(result.current.liveTokensPerSecond).toBe(firstReading);

    // Advance just under 1 s — still no update.
    act(() => {
      vi.advanceTimersByTime(900);
    });
    expect(result.current.liveTokensPerSecond).toBe(firstReading);

    // Cross the 1 s boundary: now the interval fires, picking up the
    // latest message snapshot from the refs and producing a fresh
    // reading.
    act(() => {
      vi.advanceTimersByTime(200); // total = 1100 ms
    });
    expect(result.current.liveTokensPerSecond).not.toBe(firstReading);
    expect(result.current.liveTokensPerSecond).not.toBeNull();
  });

  it('clears the reading and resets subagent tokens when isRunning flips false', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      assistantMessage({ id: 'a1', created: t0 - 500, output: 100 }),
    ];

    const { result, rerender, setSubagentTokens } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map([['k', { output: 10, created: t0 - 200 }]]),
      isRunning: true,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.liveTokensPerSecond).not.toBeNull();
    setSubagentTokens.mockClear();

    rerender({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map([['k', { output: 10, created: t0 - 200 }]]),
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.liveTokensPerSecond).toBeNull();
    // The cleanup arm calls setSubagentTokens with an updater that
    // returns an empty map when the prior map was non-empty.
    expect(setSubagentTokens).toHaveBeenCalledTimes(1);
    const updater = setSubagentTokens.mock.calls[0][0] as (
      prev: SubagentTokenMap,
    ) => SubagentTokenMap;
    const cleared = updater(new Map([['k', { output: 10, created: t0 - 200 }]]));
    expect(cleared.size).toBe(0);
  });

  it('skips in-flight assistant messages while a permission prompt is pending', () => {
    const t0 = Date.now();
    const user = userMessage('u1', t0 - 5_000);
    // Completed message from earlier in the run + an in-flight one.
    const messages: Message[] = [
      user,
      assistantMessage({ id: 'a1', created: t0 - 4_000, completed: t0 - 3_000, output: 100 }),
      assistantMessage({ id: 'a2', created: t0 - 200, output: 80 }),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      isRunning: true,
      pendingPermission: { id: 'p1', toolName: 't', input: {} } as unknown as null,
      pendingQuestion: null,
    });

    // Only the completed message contributes: 100 tokens / 1 s = 100 tps.
    expect(result.current.liveTokensPerSecond).toBeCloseTo(100, 5);
  });
});
