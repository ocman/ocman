// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import type { Message } from '../../lib/api';
import { useSessionStatus } from './useSessionStatus';
import type { SubagentTokenMap } from './useSubagentTracking';

// Minimal user / assistant message builders. Only the fields the
// hook reads (`role`, `finish`, `error`, `time`) are populated.
function userMessage(id: string, created: number): Message {
  return {
    id,
    sessionId: 's1',
    timeCreated: created,
    data: { role: 'user', time: { created } },
  };
}

function streamingAssistantMessage(id: string, created: number): Message {
  return {
    id,
    sessionId: 's1',
    timeCreated: created,
    data: {
      role: 'assistant',
      tokens: { input: 0, output: 0 },
      time: { created },
    },
  };
}

function finishedAssistantMessage(id: string, created: number): Message {
  return {
    id,
    sessionId: 's1',
    timeCreated: created,
    data: {
      role: 'assistant',
      finish: 'stop',
      time: { created, completed: created + 100 },
    },
  };
}

function erroredAssistantMessage(id: string, created: number): Message {
  return {
    id,
    sessionId: 's1',
    timeCreated: created,
    data: {
      role: 'assistant',
      error: { message: 'boom' } as unknown as never,
      time: { created, completed: created + 100 },
    },
  };
}

interface HookProps {
  lastMsg: Message | null;
  messages: Message[];
  subagentTokens: SubagentTokenMap;
  isRunning: boolean;
  pendingPermission: null;
  pendingQuestion: null;
  lastSseEventAt?: number | null;
}

function buildHook(initial: HookProps) {
  const setSubagentTokens = vi.fn();
  return renderHook(
    (props: HookProps) =>
      useSessionStatus({
        lastMsg: props.lastMsg,
        messages: props.messages,
        subagentTokens: props.subagentTokens,
        setSubagentTokens: (next) => setSubagentTokens(next),
        isRunning: props.isRunning,
        pendingPermission: props.pendingPermission,
        pendingQuestion: props.pendingQuestion,
        lastSseEventAt: props.lastSseEventAt ?? null,
      }),
    { initialProps: initial },
  );
}

describe('useSessionStatus lastSseEventAt', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2024-01-01T00:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('upgrades waiting → busy when an SSE event landed in the last 500ms', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      finishedAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
      // Event arrived 200ms ago — within the 500ms window.
      lastSseEventAt: t0 - 200,
    });

    expect(result.current.rawOptimisticStatus).toBe('waiting');
    expect(result.current.optimisticStatus).toBe('busy');
  });

  it('does NOT upgrade when the SSE event is older than 500ms', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      finishedAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
      // Event arrived 800ms ago — outside the 500ms window.
      lastSseEventAt: t0 - 800,
    });

    expect(result.current.optimisticStatus).toBe('waiting');
  });

  it('flips back to waiting once the 500ms window expires', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      finishedAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
      lastSseEventAt: t0,
    });

    expect(result.current.optimisticStatus).toBe('busy');

    // Advance past the 500ms window. The hook must re-render to flip
    // back to waiting on its own (no input changes, just time
    // elapsed).
    act(() => {
      vi.advanceTimersByTime(600);
    });

    expect(result.current.optimisticStatus).toBe('waiting');
  });

  it('does not upgrade error to busy', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      erroredAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
      lastSseEventAt: t0 - 100,
    });

    expect(result.current.rawOptimisticStatus).toBe('error');
    expect(result.current.optimisticStatus).toBe('error');
  });

  it('does not upgrade done (no last message) to busy', () => {
    const t0 = Date.now();
    const { result } = buildHook({
      lastMsg: null,
      messages: [],
      subagentTokens: new Map(),
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
      lastSseEventAt: t0 - 100,
    });

    expect(result.current.rawOptimisticStatus).toBe('done');
    expect(result.current.optimisticStatus).toBe('done');
  });

  it('leaves busy alone (no debounce side-effect)', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      streamingAssistantMessage('a1', t0 - 200),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      isRunning: true,
      pendingPermission: null,
      pendingQuestion: null,
      lastSseEventAt: t0 - 100,
    });

    expect(result.current.rawOptimisticStatus).toBe('busy');
    expect(result.current.optimisticStatus).toBe('busy');
  });
});
