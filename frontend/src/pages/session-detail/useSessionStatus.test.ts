// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import type { Message, SessionStatus } from '../../lib/api';
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
  sessionStatus?: SessionStatus;
  awaitingAssistantResponse?: boolean;
  recentWorkEventAt?: number | null;
  isRunning: boolean;
  pendingPermission: null;
  pendingQuestion: null;
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
        sessionStatus: props.sessionStatus,
        awaitingAssistantResponse: props.awaitingAssistantResponse,
        recentWorkEventAt: props.recentWorkEventAt ?? null,
        isRunning: props.isRunning,
        pendingPermission: props.pendingPermission,
        pendingQuestion: props.pendingQuestion,
      }),
    { initialProps: initial },
  );
}

describe('useSessionStatus', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2024-01-01T00:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns waiting for a finished assistant message', () => {
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
    });

    expect(result.current.optimisticStatus).toBe('waiting');
  });

  it('returns error for an errored assistant message', () => {
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
    });

    expect(result.current.optimisticStatus).toBe('error');
  });

  it('returns done when there is no last message', () => {
    const { result } = buildHook({
      lastMsg: null,
      messages: [],
      subagentTokens: new Map(),
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

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
    });

    expect(result.current.optimisticStatus).toBe('busy');
  });

  it('shows busy while awaiting the first assistant response after a user send', () => {
    const t0 = Date.now();
    const messages: Message[] = [userMessage('u1', t0 - 100)];

    const { result } = buildHook({
      lastMsg: messages[0],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'done',
      awaitingAssistantResponse: true,
      isRunning: true,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.optimisticStatus).toBe('busy');
  });

  it('treats server-reported busy as busy even when the last assistant message finished', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      finishedAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'busy',
      awaitingAssistantResponse: false,
      isRunning: true,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.optimisticStatus).toBe('busy');
  });

  it('treats recent work events as busy during tool/subagent gaps', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      finishedAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'waiting',
      awaitingAssistantResponse: false,
      recentWorkEventAt: t0 - 100,
      isRunning: true,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.optimisticStatus).toBe('busy');
  });

  it('shows done immediately for an old finished conversation when session status is done', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 20_000),
      finishedAssistantMessage('a1', t0 - 19_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'done',
      awaitingAssistantResponse: false,
      recentWorkEventAt: null,
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.optimisticStatus).toBe('done');
  });

  it('drops back to waiting as soon as the work-event window expires', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      finishedAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'waiting',
      awaitingAssistantResponse: false,
      recentWorkEventAt: t0,
      isRunning: true,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.optimisticStatus).toBe('busy');

    act(() => {
      vi.advanceTimersByTime(600);
    });

    // No grace window: the transition lands as soon as the work-event
    // window expires. The badge no longer holds a stale 'busy'.
    expect(result.current.optimisticStatus).toBe('waiting');
  });
  // #488: an interrupted session's last message is an unfinished assistant
  // turn, which every local heuristic reads as "still streaming". The
  // backend already knows the process that owned it is gone, so the badge
  // must follow the backend and stop spinning.
  it('reports interrupted instead of spinning on a lost turn', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 60_000),
      streamingAssistantMessage('a1', t0 - 59_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'interrupted',
      awaitingAssistantResponse: false,
      recentWorkEventAt: null,
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.optimisticStatus).toBe('interrupted');
  });

  it('lets a fresh prompt outrank a stale interrupted status', () => {
    const t0 = Date.now();
    const messages: Message[] = [userMessage('u1', t0 - 100)];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'interrupted',
      awaitingAssistantResponse: true,
      recentWorkEventAt: null,
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.optimisticStatus).toBe('busy');
  });

  it('still reports error when an interrupted session also errored', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      erroredAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'interrupted',
      awaitingAssistantResponse: false,
      recentWorkEventAt: null,
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.optimisticStatus).toBe('error');
  });
});
